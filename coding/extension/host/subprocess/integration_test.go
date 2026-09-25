package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

const (
	envFixtureExtBin = "PIG_TEST_FIXTURE_EXT_BIN"
	envSDKFixtureBin = "PIG_TEST_SDK_FIXTURE_BIN"
)

func mustUsePrebuilt(t *testing.T, envKey string) (string, bool) {
	t.Helper()
	if p := os.Getenv(envKey); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s=%q does not exist: %v", envKey, p, err)
		}
		return p, true
	}
	return "", false
}

func testExtensionBinaryPath(dir, name string) string {
	return filepath.Join(dir, name+strings.TrimPrefix(extensionArtifactName(runtime.GOOS, "go"), "bin"))
}

// buildFixture compiles the fixture extension binary into tmpDir and returns
// the path. Shared by all integration tests.
func buildFixture(t *testing.T) string {
	t.Helper()
	if p, ok := mustUsePrebuilt(t, envFixtureExtBin); ok {
		return p
	}
	modRoot := findModuleRoot(t)
	tmpDir := t.TempDir()
	binPath := testExtensionBinaryPath(tmpDir, "fixture-ext")

	// Build has its own generous timeout separate from the test context,
	// because under heavy parallel execution (-p 12 -count=3) the Go
	// build cache is contended and compilation alone can take 30s+.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath,
		"./coding/extension/host/subprocess/testdata/fixture-ext/")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	return binPath
}

// shortSockDir returns an isolated short socket directory path (macOS
// 104-byte Unix socket limit). Each test gets its own /tmp child: using a
// shared /tmp/pig-test directory made subprocess tests flaky under package
// parallelism because one package's cleanup could remove another package's
// live listener socket.
func shortSockDir(t *testing.T) string {
	t.Helper()
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	sockDir, err := os.MkdirTemp(parent, "pig-test-*")
	if err != nil {
		t.Fatalf("mkdir short socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	t.Setenv("XDG_RUNTIME_DIR", sockDir)
	t.Setenv("TMPDIR", sockDir)
	t.Setenv("TMP", sockDir)
	t.Setenv("TEMP", sockDir)
	return sockDir
}

// newTestHost creates a Host rooted in a test directory.
func newTestHost(t *testing.T) *Host {
	t.Helper()
	h := NewHost(t.TempDir())
	return h
}

// Under heavy parallel execution (go test -p 12 ./... -count=3), CPU
// starvation makes fixed 2-5s deadlines flaky. This helper returns a
// minimum of minDur, scaled up if the test's own deadline is generous.
// For tests invoked with -timeout, the go test framework sets a deadline
// on the test context; we use that as a signal for available budget.
func testTimeout(t *testing.T, minDur time.Duration) time.Duration {
	t.Helper()
	// Under contention, subprocess IPC (Node startup + JSON-RPC) can be
	// 10-20x slower than uncontended. Scale the minimum by 5x to absorb
	// scheduler jitter without making tests unnecessarily slow in fast
	// environments. Node overlay startup (tsx transpile + a dynamic
	// @earendil-works/pi-coding-agent import) has been observed to need more
	// than 45s on a cold, memory-pressured CI container, so the floor here is
	// what keeps TSCustomOverlay and its siblings deterministic there.
	return max(minDur, 5*minDur)
}

// pollUntil spins until cond() returns true or timeout expires.
// Uses exponential backoff starting at 10ms to reduce CPU waste during
// contended parallel runs while still responding quickly in fast environments.
func pollUntil(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	sleep := 10 * time.Millisecond
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("pollUntil timed out after %v: %s", timeout, msg)
		}
		time.Sleep(sleep)
		sleep = min(sleep*2, 200*time.Millisecond)
	}
}

// ── Gap 1: Runner integration ────────────────────────────────────────────────

// TestHost_Integration_RunnerToolsSurface proves the full chain:
// exact source → Host.Load → extension.Extension → inproc.Runner.Tools()
// contains the subprocess tool. This is 's primary acceptance criterion.
func TestHost_Integration_RunnerToolsSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildFixture(t)
	shortSockDir(t)

	h := newTestHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout(t, 15*time.Second))
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Feed the extension into inproc.Runner: exactly what cmd/pig does.
	runner := inproc.NewRunner([]extension.Extension{*ext}, "/tmp")

	// Runner.Tools() must include the subprocess tool.
	tools := runner.Tools()
	found := false
	for _, tool := range tools {
		if tool.Definition.Name == "greet" {
			found = true
			if tool.Definition.Description != "Says hello" {
				t.Errorf("tool description = %q", tool.Definition.Description)
			}
			if tool.Definition.Execute == nil {
				t.Error("tool Execute is nil")
			}
			break
		}
	}
	if !found {
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Definition.Name
		}
		t.Fatalf("Runner.Tools() does not include 'greet'. Got: %v", names)
	}

	// Runner.Commands() must include the subprocess command.
	cmds := runner.Commands()
	cmdFound := false
	for _, cmd := range cmds {
		if cmd.Name == "hello" {
			cmdFound = true
			break
		}
	}
	if !cmdFound {
		t.Error("Runner.Commands() does not include 'hello'")
	}

	h.Shutdown("test done")
}

func TestHostLoadsSourceExtensionWithBrokenVCSMetadata(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "fixture-ext", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/broken-vcs-extension\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "--quiet")
	git.Dir = root
	git.Env = withoutGitEnvironmentOverrides(os.Environ())
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "index"), []byte("broken index"), 0o644); err != nil {
		t.Fatal(err)
	}

	shortSockDir(t)
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	defer host.Shutdown("test done")
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout(t, 15*time.Second))
	defer cancel()
	ext, err := host.Load(ctx, ExtConfig{Name: "fixture", Source: root, Enabled: true})
	if err != nil {
		t.Fatalf("Load source extension: %v", err)
	}
	if _, ok := ext.Tools["greet"]; !ok {
		t.Fatalf("loaded extension tools = %v, want greet", reflect.ValueOf(ext.Tools).MapKeys())
	}
}

// ── Gap 2: Graceful shutdown ─────────────────────────────────────────────────

// TestHost_Integration_GracefulShutdown proves the extension receives the
// shutdown message and exits cleanly (process exit code 0).
func TestHost_Integration_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildFixture(t)
	shortSockDir(t)

	h := newTestHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Get the managed ext to check its process.
	h.mu.Lock()
	me := h.exts["fixture"]
	h.mu.Unlock()

	if me == nil || me.proc == nil {
		t.Fatal("managed ext or process is nil")
	}

	// Shutdown sends shutdown message + closes.
	h.Shutdown("test done")

	// Wait for process to exit (it should have received shutdown and returned).
	proc := me.proc
	pollUntil(t, testTimeout(t, 3*time.Second),
		fmt.Sprintf("extension process (pid %d) did not exit after shutdown", proc.Pid),
		func() bool {
			return proc.Signal(os.Signal(nil)) != nil
		})
}

// ── Gap 4 & 5: Widget push + UI call through socket ──────────────────────────

// TestHost_Integration_WidgetPushAndUICall proves the full socket-level path:
// 1. Extension pushes widget_push → Host routes to UIBridge → PushProxy has lines
// 2. Extension calls ui.notify → Host routes to UIBridge → UIHandler.Notify called
// 3. Tool execution triggers both (fixture sends notify + widget_push during tool_call)
func TestHost_Integration_WidgetPushAndUICall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildFixture(t)
	shortSockDir(t)

	// Track UI calls.
	var mu sync.Mutex
	var notifyCalls []string

	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, level string) {
		mu.Lock()
		notifyCalls = append(notifyCalls, msg+":"+level)
		mu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	h.SetUIBridge(bridge)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The fixture pushes an initial widget_push("status", ["fixture: ready"])
	// during startup (after receiving ready). Give it time to arrive.
	pollUntil(t, testTimeout(t, 2*time.Second), "initial widget push did not arrive", func() bool {
		return bridge.GetWidget("fixture", "status") != nil
	})

	// Gap 4: Verify initial widget push arrived through the socket.
	proxy := bridge.GetWidget("fixture", "status")
	if proxy == nil {
		t.Fatal("initial widget push did not arrive: proxy is nil")
	}
	lines := proxy.Render(80)
	if len(lines) != 1 || lines[0] != "fixture: ready" {
		t.Errorf("initial widget = %v, want [fixture: ready]", lines)
	}

	// Execute a tool: fixture sends ui.notify + widget_push + response.
	params := json.RawMessage(`{"name":"pig"}`)
	result, err := ext.Tools["greet"].Definition.Execute(ctx, "tc-1", params, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	tr, ok := result.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("result type = %T, want agent.AgentToolResult", result)
	}
	if tr.Content != "Hello, pig!" {
		t.Errorf("content = %q", tr.Content)
	}

	// Gap 5: Verify ui.notify was called through the socket.
	pollUntil(t, testTimeout(t, 2*time.Second), "notify was never called through socket", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notifyCalls) > 0
	})
	mu.Lock()
	notifyCount := len(notifyCalls)
	var lastNotify string
	if notifyCount > 0 {
		lastNotify = notifyCalls[notifyCount-1]
	}
	mu.Unlock()

	if notifyCount == 0 {
		t.Error("ui.notify was never called: extension→host call did not arrive")
	} else if lastNotify != "Greeting pig:info" {
		t.Errorf("notify = %q, want %q", lastNotify, "Greeting pig:info")
	}

	// Verify widget was updated after tool execution.
	pollUntil(t, testTimeout(t, 2*time.Second), "widget not updated after tool execution", func() bool {
		lines := proxy.Render(80)
		return len(lines) == 1 && lines[0] == "fixture: greeted pig"
	})

	h.Shutdown("test done")
}

func TestHostIntegrationPreservesGenericExtensionProvidedEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the neutral subprocess fixture")
	}
	binPath := buildFixture(t)
	shortSockDir(t)
	host := newTestHost(t)
	host.SetUIBridge(NewUIBridge(func() {}))
	t.Cleanup(func() { host.Shutdown("test done") })
	ext, err := host.Load(t.Context(), ExtConfig{Name: "fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	registered, ok := ext.Tools["edit"]
	if !ok {
		t.Fatalf("generic extension tool edit was filtered from registration: %v", ext.Tools)
	}
	result, err := registered.Definition.Execute(t.Context(), "edit-call", json.RawMessage(`{"name":"neutral"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	toolResult, ok := result.(agent.AgentToolResult)
	if !ok || toolResult.Content != "Hello, neutral!" {
		t.Fatalf("generic extension edit result = %#v", result)
	}
}

// ── Gap 1 revisited: Full tool dispatch roundtrip ────────────────────────────

// TestHost_Integration_SpawnAndRegister is the original integration test,
// kept for regression coverage. Tests spawn → register → tool execute → command
// → event → shutdown.
func TestHost_Integration_SpawnAndRegister(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildFixture(t)
	shortSockDir(t)

	h := newTestHost(t)
	// Wire UIBridge so widget pushes don't get dropped.
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if ext == nil {
		t.Fatal("ext is nil")
		return
	}

	// Check tools.
	greet, ok := ext.Tools["greet"]
	if !ok {
		t.Fatal("missing 'greet' tool")
	}
	if greet.Definition.Name != "greet" {
		t.Errorf("tool name = %q", greet.Definition.Name)
	}
	if greet.Definition.Description != "Says hello" {
		t.Errorf("tool description = %q", greet.Definition.Description)
	}
	if greet.Definition.Execute == nil {
		t.Fatal("tool Execute is nil")
	}

	// Check commands.
	helloCmd, ok := ext.Commands["hello"]
	if !ok {
		t.Fatal("missing 'hello' command")
	}
	if helloCmd.Description != "Greet from extension" {
		t.Errorf("command description = %q", helloCmd.Description)
	}

	// Check handlers.
	if len(ext.Handlers["session_start"]) != 1 {
		t.Errorf("session_start handlers = %d, want 1", len(ext.Handlers["session_start"]))
	}

	// Execute the tool.
	params := json.RawMessage(`{"name":"pig"}`)
	result, err := greet.Definition.Execute(ctx, "tc-1", params, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	tr, ok := result.(agent.AgentToolResult)
	if !ok {
		t.Fatalf("result type = %T, want agent.AgentToolResult", result)
	}
	if tr.Content != "Hello, pig!" {
		t.Errorf("content = %q, want %q", tr.Content, "Hello, pig!")
	}

	// Execute the command.
	if helloCmd.Handler != nil {
		err = helloCmd.Handler(ctx, "test args")
		if err != nil {
			t.Fatalf("command handler: %v", err)
		}
	}

	// Dispatch an event.
	if len(ext.Handlers["session_start"]) > 0 {
		handler := ext.Handlers["session_start"][0]
		_, err := handler(map[string]any{"reason": "startup"})
		if err != nil {
			t.Fatalf("session_start handler: %v", err)
		}
	}

	h.Shutdown("test done")
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// findModuleRoot walks up from cwd to find the go.mod file.
func findModuleRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

// testUIContext implements extension.UIContext for testing by embedding the
// noop and overriding Notify and SetStatus with callback hooks.
type testUIContext struct {
	extension.UIContext // embed NoopUIContext
	onNotify            func(msg, level string)
	onSetWorkingVisible func(visible bool)
	onStatus            func(key, text string)

	// terminalInput captures the raw-input handler the bridge registers, so a
	// test can feed keystrokes through the real host→extension path.
	terminalInputMu sync.Mutex
	terminalInput   extension.RemoteTerminalInputHandler

	// stateMu guards the UI-state fields below. An extension's handleIncoming
	// dispatches each host call on its own goroutine, so these setters can run
	// concurrently: mirroring the real specialLinesComponent, which is mutex
	// guarded. Tests read a locked snapshot via uiSnapshot.
	stateMu            sync.Mutex
	workingIndicators  []any
	hiddenLabels       []string
	footerCleared      bool
	headerCleared      bool
	editorCleared      bool
	footerLines        []string
	headerLines        []string
	editorLabel        string
	editorBorderPrefix string
	editorHistory      []string
	autocompleteSource extension.AsyncSuggestionSource
	allThemes          []extension.ThemeMeta
	themeByName        map[string]extension.Theme

	uiStateMu     sync.Mutex
	editorText    string
	toolsExpanded bool

	overlayMu     sync.Mutex
	overlayHandle *testOverlayHandle
	overlayHost   extension.RemoteOverlayHost
	overlayOpts   extension.RemoteOverlayOptions
	overlayOpenCh chan struct{}
}

func newTestUIContext() *testUIContext {
	return &testUIContext{UIContext: extension.NoopUIContext, overlayOpenCh: make(chan struct{}, 1)}
}

func (h *testUIContext) OnRemoteTerminalInput(_ string, handler extension.RemoteTerminalInputHandler) func() {
	h.terminalInputMu.Lock()
	h.terminalInput = handler
	h.terminalInputMu.Unlock()
	return func() {
		h.terminalInputMu.Lock()
		h.terminalInput = nil
		h.terminalInputMu.Unlock()
	}
}

// sendTerminalInput feeds one chunk to the registered handler, reporting
// whether an extension consumed it. ok is false when nothing is subscribed.
func (h *testUIContext) sendTerminalInput(data string) (consumed, ok bool) {
	h.terminalInputMu.Lock()
	handler := h.terminalInput
	h.terminalInputMu.Unlock()
	if handler == nil {
		return false, false
	}
	return handler(context.Background(), data).Consume, true
}

func (h *testUIContext) Notify(msg, level string) {
	if h.onNotify != nil {
		h.onNotify(msg, level)
	}
}

func (h *testUIContext) SetStatus(key, text string) {
	if h.onStatus != nil {
		h.onStatus(key, text)
	}
}

func (h *testUIContext) SetWorkingIndicator(opts extension.WorkingIndicatorOptions) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.workingIndicators = append(h.workingIndicators, opts)
}

func (h *testUIContext) SetHiddenThinkingLabel(label string) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	h.hiddenLabels = append(h.hiddenLabels, label)
}

func (h *testUIContext) SetFooter(factory any) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if frame, ok := factory.(extension.WidthLines); ok {
		factory = frame.Lines
	}
	if lines, ok := factory.([]string); ok {
		h.footerLines = append([]string(nil), lines...)
		return
	}
	if factory == nil {
		h.footerCleared = true
	}
}

func (h *testUIContext) SetHeader(factory any) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if frame, ok := factory.(extension.WidthLines); ok {
		factory = frame.Lines
	}
	if lines, ok := factory.([]string); ok {
		h.headerLines = append([]string(nil), lines...)
		return
	}
	if factory == nil {
		h.headerCleared = true
	}
}
func (h *testUIContext) SetLogin(extension.LoginDefinition) error { return nil }

func (h *testUIContext) SetEditorComponent(factory any) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if frame, ok := factory.(extension.WidthLines); ok {
		factory = frame.Lines
	}
	if lines, ok := factory.([]string); ok {
		h.footerLines = append([]string(nil), lines...)
		return
	}
	if factory == nil {
		h.editorCleared = true
		return
	}
	// Subprocess shim now ships a structured decoration payload.
	// Record what the bridge handed us so tests can assert end-to-end
	// that the setEditorComponent factory reached the host with the
	// captured ANSI prefix/suffix and history entries intact.
	if payload, ok := factory.(map[string]any); ok {
		if deco, ok := payload["decoration"].(map[string]any); ok {
			h.editorLabel, _ = deco["label"].(string)
			h.editorBorderPrefix, _ = deco["borderPrefix"].(string)
		}
		if hist, ok := payload["history"].([]string); ok {
			h.editorHistory = append([]string(nil), hist...)
		}
	}
}

// uiStateSnapshot is a lock-free copy of testUIContext's concurrently-mutated
// UI state, for tests to assert against without racing the dispatch goroutines.
type uiStateSnapshot struct {
	workingIndicators  int
	hiddenLabels       []string
	footerCleared      bool
	headerCleared      bool
	editorCleared      bool
	footerLines        []string
	headerLines        []string
	editorLabel        string
	editorBorderPrefix string
	editorHistory      []string
}

func (h *testUIContext) uiSnapshot() uiStateSnapshot {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return uiStateSnapshot{
		workingIndicators:  len(h.workingIndicators),
		hiddenLabels:       append([]string(nil), h.hiddenLabels...),
		footerCleared:      h.footerCleared,
		headerCleared:      h.headerCleared,
		editorCleared:      h.editorCleared,
		footerLines:        append([]string(nil), h.footerLines...),
		headerLines:        append([]string(nil), h.headerLines...),
		editorLabel:        h.editorLabel,
		editorBorderPrefix: h.editorBorderPrefix,
		editorHistory:      append([]string(nil), h.editorHistory...),
	}
}

func (h *testUIContext) GetAllThemes() []extension.ThemeMeta { return h.allThemes }
func (h *testUIContext) SetWorkingVisible(visible bool) {
	if h.onSetWorkingVisible != nil {
		h.onSetWorkingVisible(visible)
	}
}
func (h *testUIContext) GetEditorText() string {
	h.uiStateMu.Lock()
	defer h.uiStateMu.Unlock()
	return h.editorText
}
func (h *testUIContext) GetToolsExpanded() bool {
	h.uiStateMu.Lock()
	defer h.uiStateMu.Unlock()
	return h.toolsExpanded
}
func (h *testUIContext) setUIState(text string, expanded bool) {
	h.uiStateMu.Lock()
	defer h.uiStateMu.Unlock()
	h.editorText, h.toolsExpanded = text, expanded
}

func (h *testUIContext) GetTheme(name string) (extension.Theme, error) {
	if h.themeByName == nil {
		return nil, nil
	}
	return h.themeByName[name], nil
}

// testOverlayHandle is the handle returned by testUIContext.RunRemoteOverlay.
// It records pushed lines and unblocks the originating call when Close is
// invoked.
type testOverlayHandle struct {
	mu      sync.Mutex
	lines   []string
	updates int
	result  any
	closed  chan struct{}
}

func newTestOverlayHandle() *testOverlayHandle {
	return &testOverlayHandle{closed: make(chan struct{})}
}

func (h *testOverlayHandle) UpdateLines(lines []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append([]string(nil), lines...)
	h.updates++
}

func (h *testOverlayHandle) Close(result any) {
	h.mu.Lock()
	if h.result == nil {
		h.result = result
	}
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
	h.mu.Unlock()
}

func (h *testOverlayHandle) Lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.lines))
	copy(out, h.lines)
	return out
}

func (h *testOverlayHandle) Done() <-chan struct{} { return h.closed }

func (h *testOverlayHandle) UpdateCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updates
}

// RunRemoteOverlay records the handle/host and blocks until the host signals
// close. Mirrors the real ExtUIContext.RunRemoteOverlay contract in a way
// that lets tests drive the protocol without a real TTY.
func (h *testUIContext) AddAutocompleteProvider(factory extension.AutocompleteProviderFactory) {
	if src, ok := factory.(extension.AsyncSuggestionSource); ok {
		h.stateMu.Lock()
		h.autocompleteSource = src
		h.stateMu.Unlock()
	}
}

func (h *testUIContext) autocompleteProvider() extension.AsyncSuggestionSource {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.autocompleteSource
}

func (h *testUIContext) RunRemoteOverlay(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	handle := newTestOverlayHandle()
	if onHandle != nil {
		onHandle(handle)
	}
	h.overlayMu.Lock()
	h.overlayOpts = opts
	h.overlayHandle = handle
	h.overlayHost = host
	h.overlayMu.Unlock()
	select {
	case h.overlayOpenCh <- struct{}{}:
	default:
	}
	<-handle.closed
	handle.mu.Lock()
	res := handle.result
	handle.mu.Unlock()
	return res, true
}

func (h *testUIContext) awaitOverlay(t *testing.T, timeout time.Duration) (*testOverlayHandle, extension.RemoteOverlayHost) {
	t.Helper()
	select {
	case <-h.overlayOpenCh:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for ui.custom overlay open")
	}
	h.overlayMu.Lock()
	defer h.overlayMu.Unlock()
	return h.overlayHandle, h.overlayHost
}

// ── Gap 3: Crash supervisor integration ──────────────────────────────────────

// TestHost_Integration_CrashDetection proves that when a subprocess extension
// exits unexpectedly, the Host detects the crash and invokes the supervisor's
// crash recording (exponential backoff / circuit breaker).
func TestHost_Integration_CrashDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Build the crashing fixture.
	modRoot := findModuleRoot(t)
	tmpDir := t.TempDir()
	binPath := testExtensionBinaryPath(tmpDir, "crash-ext")

	cmd := exec.Command("go", "build", "-o", binPath,
		"./coding/extension/host/subprocess/testdata/crash-ext/")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, out)
	}

	shortSockDir(t)

	h := newTestHost(t)

	// Track crash notifications.
	var crashMu sync.Mutex
	var crashes []struct {
		name     string
		delay    time.Duration
		disabled bool
		reason   string
	}
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		crashMu.Lock()
		crashes = append(crashes, struct {
			name     string
			delay    time.Duration
			disabled bool
			reason   string
		}{name, delay, disabled, reason})
		crashMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err = h.Load(ctx, ExtConfig{
		Name:    "crash",
		Path:    binPath,
		Enabled: true,
		SupervisorConfig: SupervisorConfig{
			MaxCrashes:    3,
			CrashWindow:   60 * time.Second,
			InitialDelay:  50 * time.Millisecond,
			MaxDelay:      500 * time.Millisecond,
			BackoffFactor: 2.0,
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The crash-ext exits immediately after register+ready. Wait for crash detection.
	pollUntil(t, testTimeout(t, 5*time.Second), "crash was not detected: onCrash callback never called", func() bool {
		crashMu.Lock()
		defer crashMu.Unlock()
		return len(crashes) > 0
	})

	c := crashes[0]
	if c.name != "crash" {
		t.Errorf("crash name = %q, want %q", c.name, "crash")
	}
	if c.disabled {
		t.Error("should not be disabled after 1 crash (threshold is 3)")
	}
	if c.delay < 50*time.Millisecond {
		t.Errorf("delay = %v, want >= 50ms", c.delay)
	}
}

// TestHost_Integration_AutoRestartAfterCrash proves that a crashed extension is
// relaunched by the supervisor rather than staying dead until /reload. The
// crash-ext fixture exits immediately after register+ready; without automatic
// restart the host would record exactly one crash and never relaunch. With
// restart wired, the fixture is relaunched and re-crashes until the circuit
// breaker trips, so we observe multiple crashes ending in a disabled notice.
func TestHost_Integration_AutoRestartAfterCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	modRoot := findModuleRoot(t)
	tmpDir := t.TempDir()
	binPath := testExtensionBinaryPath(tmpDir, "crash-ext")
	cmd := exec.Command("go", "build", "-o", binPath,
		"./coding/extension/host/subprocess/testdata/crash-ext/")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, out)
	}

	shortSockDir(t)
	h := newTestHost(t)

	var crashMu sync.Mutex
	var crashCount int
	var disabled bool
	h.SetCrashHandler(func(name string, delay time.Duration, isDisabled bool, reason string) {
		crashMu.Lock()
		crashCount++
		if isDisabled {
			disabled = true
		}
		crashMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := h.Load(ctx, ExtConfig{
		Name:    "crash",
		Path:    binPath,
		Enabled: true,
		SupervisorConfig: SupervisorConfig{
			MaxCrashes:    3,
			CrashWindow:   60 * time.Second,
			InitialDelay:  20 * time.Millisecond,
			MaxDelay:      100 * time.Millisecond,
			BackoffFactor: 2.0,
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// A single crash means no relaunch happened; wait for the breaker to trip,
	// which requires the fixture to be relaunched and re-crash MaxCrashes times.
	pollUntil(t, testTimeout(t, 8*time.Second), "crashed extension was not relaunched (circuit breaker never tripped)", func() bool {
		crashMu.Lock()
		defer crashMu.Unlock()
		return disabled
	})

	crashMu.Lock()
	got := crashCount
	crashMu.Unlock()
	if got < 2 {
		t.Fatalf("crashCount = %d, want >= 2 (relaunch must produce repeat crashes)", got)
	}
}

// TestHost_Integration_GracefulShutdownNoCrash proves that a graceful shutdown
// does NOT trigger the crash supervisor. This is a regression test for the bug
// where Host.Shutdown → extension exits → handleIncoming records a crash.
func TestHost_Integration_GracefulShutdownNoCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildFixture(t)
	shortSockDir(t)

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)

	var crashMu sync.Mutex
	var crashCount int
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		crashMu.Lock()
		crashCount++
		crashMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := h.Load(ctx, ExtConfig{Name: "fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Graceful shutdown.
	h.Shutdown("quit")

	// Wait for handleIncoming to process the closure: assert no crash recorded.
	pollUntil(t, testTimeout(t, 2*time.Second), "graceful shutdown did not complete", func() bool {
		// The incoming handler must have returned (closed the conn).
		// Just give it enough time for the async shutdown path.
		time.Sleep(50 * time.Millisecond)
		return true // non-polling: just need the grace period
	})

	crashMu.Lock()
	if crashCount > 0 {
		t.Errorf("graceful shutdown recorded %d crash(es): should be 0", crashCount)
	}
	crashMu.Unlock()
}

// ── SDK wire format behavioral tests ─────────────────────────────────────────
//
// These tests exercise the REAL Go SDK (extensions/sdk/) through the Host,
// proving the wire format round-trip works end-to-end. The fixture-ext tests
// above use hand-rolled JSON that matches host format by construction.
// These SDK tests catch mismatches like schema→parameters or []string→[]struct
// that only surface when the SDK serializes differently from the host.

// buildSDKFixture compiles the SDK-based test fixture.
func buildSDKFixture(t *testing.T) string {
	t.Helper()
	if p, ok := mustUsePrebuilt(t, envSDKFixtureBin); ok {
		return p
	}
	tmpDir := t.TempDir()
	binPath := testExtensionBinaryPath(tmpDir, "sdk-fixture")

	// The SDK fixture has its own go.mod with a replace directive.
	// Build has its own generous timeout separate from the test context,
	// because under heavy parallel execution (-p 12 -count=3) the Go
	// build cache is contended and compilation alone can take 30s+.
	srcDir := filepath.Join("testdata", "sdk-fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build sdk-fixture: %v\n%s", err, out)
	}
	return binPath
}

// TestHost_Integration_SDKRegister proves an extension built with the Go SDK
// registers successfully through the Host. This is the behavioral test that
// would have caught the schema→parameters wire format mismatch.
func TestHost_Integration_SDKRegister(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildSDKFixture(t)
	shortSockDir(t)

	h := newTestHost(t)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "sdk-fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify tools registered with correct field mapping.
	echoTool, ok := ext.Tools["echo"]
	if !ok {
		names := make([]string, 0, len(ext.Tools))
		for name := range ext.Tools {
			names = append(names, name)
		}
		t.Fatalf("SDK extension missing 'echo' tool. Got: %v", names)
	}
	if echoTool.Definition.Description != "Echo back the input" {
		t.Errorf("tool description = %q, want %q", echoTool.Definition.Description, "Echo back the input")
	}
	if echoTool.Definition.Execute == nil {
		t.Error("tool Execute func is nil")
	}
	// Parameters (not Schema) must be non-nil: this is the exact field
	// that was mismatched (SDK sent "schema", host expected "parameters").
	if echoTool.Definition.Parameters == nil {
		t.Error("tool Parameters is nil: SDK→Host field mapping broken")
	}

	// Verify commands registered.
	pingCmd, ok := ext.Commands["ping"]
	if !ok {
		t.Fatal("SDK extension missing 'ping' command")
	}
	if pingCmd.Handler == nil {
		t.Error("command Handler is nil")
	}

	// Verify event handlers registered (was []string, must be []HandlerDecl).
	if len(ext.Handlers["session_start"]) == 0 {
		t.Error("SDK extension missing session_start handler: handler format mismatch?")
	}
}

// TestHost_Integration_SDKToolExecution proves a tool registered via the SDK
// can be invoked through the Host and returns a correct result.
func TestHost_Integration_SDKToolExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildSDKFixture(t)
	shortSockDir(t)

	h := newTestHost(t)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "sdk-fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	echoTool, ok := ext.Tools["echo"]
	if !ok {
		t.Fatal("missing echo tool")
	}

	result, err := echoTool.Definition.Execute(ctx, "tc-1",
		json.RawMessage(`{"text":"hello"}`), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

// TestHost_Integration_SDKUIParityMethods exercises the SDK UI wrappers
// through the real subprocess host bridge.
func TestHost_Integration_SDKUIParityMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildSDKFixture(t)
	shortSockDir(t)

	var notifications []string
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, level string) {
		notifications = append(notifications, msg)
	}
	fakeUI.allThemes = []extension.ThemeMeta{{Name: "dark", Path: "/tmp/dark.json"}}
	fakeUI.themeByName = map[string]extension.Theme{"dark": "dark-json"}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "sdk-fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd, ok := ext.Commands["ui-probe"]
	if !ok {
		t.Fatal("SDK extension missing ui-probe command")
	}
	if err := cmd.Handler(context.Background(), ""); err != nil {
		t.Fatalf("ui-probe: %v", err)
	}

	uiState := fakeUI.uiSnapshot()
	if uiState.workingIndicators != 1 {
		t.Fatalf("working indicator calls = %d, want 1", uiState.workingIndicators)
	}
	if len(uiState.hiddenLabels) != 1 || uiState.hiddenLabels[0] != "hidden-thoughts" {
		t.Fatalf("hidden labels = %v", uiState.hiddenLabels)
	}
	if !uiState.footerCleared || !uiState.headerCleared || !uiState.editorCleared {
		t.Fatalf("clear flags footer=%v header=%v editor=%v", uiState.footerCleared, uiState.headerCleared, uiState.editorCleared)
	}

	proxy := bridge.GetWidget("sdk-fixture", "status")
	if proxy == nil {
		t.Fatal("sdk ui-probe widget missing")
	}
	lines := proxy.Render(80)
	if len(lines) != 1 || lines[0] != "sdk-fixture: ui-probe" {
		t.Fatalf("widget lines = %v", lines)
	}
	proxy = bridge.GetWidget("sdk-fixture", "status-call")
	if proxy == nil {
		t.Fatal("sdk ui-probe call-path widget missing")
	}
	lines = proxy.Render(80)
	if len(lines) != 1 || lines[0] != "sdk-fixture: ui-probe call" {
		t.Fatalf("call-path widget lines = %v", lines)
	}

	if len(notifications) == 0 {
		t.Fatal("expected summary notification from ui-probe")
	}
	var summary struct {
		ThemesCount int    `json:"themesCount"`
		Theme       string `json:"theme"`
		ThemeErr    string `json:"themeErr"`
		CustomErr   string `json:"customErr"`
		AutoErr     string `json:"autoErr"`
		TermErr     string `json:"termErr"`
	}
	if err := json.Unmarshal([]byte(notifications[len(notifications)-1]), &summary); err != nil {
		t.Fatalf("summary notify json: %v\nraw=%q", err, notifications[len(notifications)-1])
	}
	if summary.ThemesCount != 1 || summary.Theme != "dark-json" || summary.ThemeErr != "" {
		t.Fatalf("summary theme payload = %+v", summary)
	}
	if !strings.Contains(summary.CustomErr, "unsupported") {
		t.Fatalf("customErr = %q, want unsupported (SDK no-key path)", summary.CustomErr)
	}
	if !strings.Contains(summary.AutoErr, "unsupported") {
		t.Fatalf("autoErr = %q, want unsupported", summary.AutoErr)
	}
	// onTerminalInput is implemented for subprocess extensions: the host asks
	// the extension whether to consume each chunk, bounded so a slow extension
	// cannot stall the input loop. It previously returned "unsupported", and
	// this assertion pinned that stub.
	if summary.TermErr != "" {
		t.Fatalf("termErr = %q, want a successful subscription", summary.TermErr)
	}

	// Drive real keystrokes through the host→extension round trip. The fixture
	// consumes the sentinel only, so this proves both directions of the verdict
	// across the subprocess boundary rather than just that subscribing worked.
	consumed, ok := fakeUI.sendTerminalInput("\x1b[99~")
	if !ok {
		t.Fatal("extension subscribed but the host registered no input handler")
	}
	if !consumed {
		t.Error("sentinel chunk was not consumed by the subscribed extension")
	}
	if consumed, _ := fakeUI.sendTerminalInput("a"); consumed {
		t.Error("ordinary keystroke was consumed; the editor would never see it")
	}
}

func TestHost_Integration_SDKFocusedComponentOwnsInputUntilCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildSDKFixture(t)
	shortSockDir(t)
	fakeUI := newTestUIContext()
	notified := make(chan string, 1)
	fakeUI.onNotify = func(message, _ string) { notified <- message }

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{Name: "sdk-fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cmd := ext.Commands["focused-probe"]
	commandDone := make(chan error, 1)
	go func() { commandDone <- cmd.Handler(context.Background(), "") }()

	handle, input := fakeUI.awaitOverlay(t, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines := handle.Lines()
		if len(lines) == 4 && strings.HasPrefix(lines[0], "focused width=") && lines[1] == "> alpha" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("initial focused lines = %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.NotifyWidth(91)
	deadline = time.Now().Add(5 * time.Second)
	for {
		lines := handle.Lines()
		if len(lines) == 4 && lines[0] == "focused width=91" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resized focused lines = %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	updates := handle.UpdateCount()
	h.NotifyWidth(91)
	time.Sleep(100 * time.Millisecond)
	if got := handle.UpdateCount(); got != updates {
		t.Fatalf("unchanged resize published another frame: before=%d after=%d", updates, got)
	}
	input.OnInput("\x1b[6~")
	deadline = time.Now().Add(5 * time.Second)
	for {
		lines := handle.Lines()
		if len(lines) == 4 && lines[3] == "> gamma" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("updated focused lines = %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	input.OnInput("\r")

	select {
	case err := <-commandDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("focused component command did not complete")
	}
	select {
	case message := <-notified:
		if message != "focused=gamma disposed=true" {
			t.Fatalf("notification = %q", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("focused component did not notify its result")
	}

	errorDone := make(chan error, 1)
	go func() { errorDone <- cmd.Handler(context.Background(), "") }()
	errorHandle, errorInput := fakeUI.awaitOverlay(t, 5*time.Second)
	errorInput.OnInput("e")
	select {
	case <-errorHandle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("focused component error did not close overlay")
	}
	select {
	case err := <-errorDone:
		if err == nil || !strings.Contains(err.Error(), "focused input failed") {
			t.Fatalf("focused input error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("focused component error did not return to command")
	}

	encodeDone := make(chan error, 1)
	go func() { encodeDone <- cmd.Handler(context.Background(), "") }()
	encodeHandle, encodeInput := fakeUI.awaitOverlay(t, 5*time.Second)
	encodeInput.OnInput("j")
	select {
	case <-encodeHandle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("invalid focused result did not close overlay")
	}
	select {
	case err := <-encodeDone:
		if err == nil || !strings.Contains(err.Error(), "encode focused result") {
			t.Fatalf("invalid focused result error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invalid focused result did not return to command")
	}
}

func TestHost_Integration_TSFileShim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	shortSockDir(t)

	var notifications []string
	var notifyMu sync.Mutex
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _level string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{
		Exec: func(command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
			return extension.ExecCommand(context.Background(), t.TempDir(), command, args, opts)
		},
		GetActiveTools:   func() []string { return []string{"read", "write"} },
		GetAllTools:      func() []ToolInfo { return []ToolInfo{{Name: "read"}, {Name: "write"}, {Name: "bash"}} },
		GetCommands:      func() []CommandInfo { return []CommandInfo{{Name: "help"}, {Name: "ping_ts"}, {Name: "custom_ts"}} },
		GetThinkingLevel: func() string { return "medium" },
		GetSessionName:   func() string { return "TS Fixture Session" },
		GetSessionID:     func() string { return "sess-ts-1" },
		GetSessionFile:   func() string { return "/tmp/ts-fixture.jsonl" },
		GetLeafID:        func() string { return "leaf-ts-1" },
		// The state push replicates the log incrementally, so the shim is fed
		// through bounded pages; the leaf below selects the branch.
		GetEntriesPage: func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
			all := []json.RawMessage{json.RawMessage(
				`{"type":"message","id":"leaf-ts-1","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`)}
			if cursor < 0 || cursor > len(all) {
				cursor = 0
			}
			return all[cursor:], len(all), false, "leaf-ts-1"
		},
		IsIdle:          func() bool { return true },
		GetSystemPrompt: func() string { return "ts-shim system prompt" },
		GetModelInfo: func() map[string]any {
			return map[string]any{"id": "anthropic/claude-3-5-haiku", "modelId": "claude-3-5-haiku", "provider": map[string]any{"id": "anthropic"}, "api": "anthropic-messages", "name": "Claude 3.5 Haiku", "displayName": "Claude 3.5 Haiku"}
		},
		GetModel: func(providerID, modelID string) map[string]any {
			if providerID != "anthropic" || modelID != "claude-3-5-haiku" {
				return nil
			}
			return map[string]any{"id": modelID, "modelId": modelID, "provider": providerID, "api": "anthropic-messages", "name": "Claude 3.5 Haiku", "displayName": "Claude 3.5 Haiku"}
		},
		GetModels: func() []map[string]any {
			return []map[string]any{{"id": "claude-3-5-haiku", "modelId": "claude-3-5-haiku", "provider": "anthropic", "api": "anthropic-messages", "name": "Claude 3.5 Haiku", "displayName": "Claude 3.5 Haiku"}}
		},
		GetModelAuth: func(_ context.Context, providerID, modelID string) map[string]any {
			return map[string]any{"ok": providerID == "anthropic" && modelID == "claude-3-5-haiku", "apiKey": "test-key", "headers": map[string]string{"x-test": "1"}}
		},
		Complete: func(_ context.Context, model, request, auth map[string]any) (map[string]any, error) {
			return map[string]any{"stopReason": "stop", "content": []map[string]any{{"type": "text", "text": "OK"}}}, nil
		},
		StreamModel: func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error) {
			partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "OK"}}, StopReason: ai.StopReasonPending}
			final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "OK"}}, StopReason: ai.StopReasonStop}
			stream := ai.NewAssistantMessageEventStream()
			if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
				return nil, err
			}
			if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
				return nil, err
			}
			return stream, nil
		},
		GetContextUsage: func() *extension.ContextUsage {
			tokens := 42
			pct := 4
			return &extension.ContextUsage{Tokens: &tokens, ContextWindow: 1000, Percent: &pct}
		},
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{
		Name:    "ts-fixture",
		Source:  filepath.Join("testdata", "ts-fixture.ts"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load TS fixture: %v", err)
	}

	echoTool, ok := ext.Tools["echo_ts"]
	if !ok {
		t.Fatal("missing echo_ts tool")
	}
	result, err := echoTool.Definition.Execute(ctx, "tc-ts-1", json.RawMessage(`{"text":"hello"}`), nil)
	if err != nil {
		t.Fatalf("Execute echo_ts: %v", err)
	}
	resJSON, _ := json.Marshal(result)
	var res struct {
		Content string `json:"Content"`
		Details struct {
			Source               string   `json:"source"`
			ActiveTools          []string `json:"activeTools"`
			AllTools             []string `json:"allTools"`
			Commands             []string `json:"commands"`
			ThinkingLevel        string   `json:"thinkingLevel"`
			IsIdle               bool     `json:"isIdle"`
			HasPendingMessages   bool     `json:"hasPendingMessages"`
			SystemPrompt         string   `json:"systemPrompt"`
			SessionID            string   `json:"sessionId"`
			SessionName          string   `json:"sessionName"`
			SessionFile          string   `json:"sessionFile"`
			LeafID               string   `json:"leafId"`
			BranchCount          int      `json:"branchCount"`
			EntryCount           int      `json:"entryCount"`
			BuiltContextMessages int      `json:"builtContextMessages"`
			ModelID              string   `json:"modelId"`
			ModelAPI             string   `json:"modelApi"`
			FoundModelID         string   `json:"foundModelId"`
			AuthOK               bool     `json:"authOk"`
			ExecStdout           string   `json:"execStdout"`
			ExecCode             int      `json:"execCode"`
			MissingExec          *struct {
				Stdout string `json:"stdout"`
				Stderr string `json:"stderr"`
				Code   int    `json:"code"`
				Killed bool   `json:"killed"`
			} `json:"missingExec"`
			CompletionStopReason string `json:"completionStopReason"`
			CompletionText       string `json:"completionText"`
			ContextUsage         struct {
				Tokens        int     `json:"tokens"`
				ContextWindow int     `json:"contextWindow"`
				Percent       float64 `json:"percent"`
			} `json:"contextUsage"`
		} `json:"Details"`
	}
	_ = json.Unmarshal(resJSON, &res)
	if res.Content != "echo-ts: hello" {
		t.Fatalf("content = %q, want %q (raw=%s)", res.Content, "echo-ts: hello", resJSON)
	}
	if !reflect.DeepEqual(res.Details.ActiveTools, []string{"read", "write"}) {
		t.Errorf("activeTools = %v, want [read write] (raw=%s)", res.Details.ActiveTools, resJSON)
	}
	if !reflect.DeepEqual(res.Details.AllTools, []string{"read", "write", "bash"}) {
		t.Errorf("allTools = %v, want [read write bash]", res.Details.AllTools)
	}
	if !reflect.DeepEqual(res.Details.Commands, []string{"help", "ping_ts", "custom_ts"}) {
		t.Errorf("commands = %v, want [help ping_ts custom_ts]", res.Details.Commands)
	}
	if res.Details.ThinkingLevel != "medium" {
		t.Errorf("thinkingLevel = %q, want medium", res.Details.ThinkingLevel)
	}
	if !res.Details.IsIdle {
		t.Errorf("isIdle = false, want true")
	}
	if res.Details.SystemPrompt != "ts-shim system prompt" {
		t.Errorf("systemPrompt = %q, want %q", res.Details.SystemPrompt, "ts-shim system prompt")
	}
	if res.Details.SessionID != "sess-ts-1" || res.Details.SessionFile != "/tmp/ts-fixture.jsonl" || res.Details.LeafID != "leaf-ts-1" {
		t.Errorf("session state = %+v", res.Details)
	}
	if res.Details.EntryCount != 1 || res.Details.BranchCount != 1 || res.Details.BuiltContextMessages != 1 {
		t.Errorf("entry/branch counts = %d/%d/%d, want 1/1/1", res.Details.EntryCount, res.Details.BranchCount, res.Details.BuiltContextMessages)
	}
	if res.Details.ModelID != "claude-3-5-haiku" || res.Details.ModelAPI != "anthropic-messages" || res.Details.FoundModelID != "claude-3-5-haiku" {
		t.Errorf("model details = %+v", res.Details)
	}
	if !res.Details.AuthOK || res.Details.ExecStdout != "exec-ts" || res.Details.ExecCode != 0 || res.Details.CompletionStopReason != "stop" || res.Details.CompletionText != "OK" {
		t.Errorf("auth/exec/complete details = %+v", res.Details)
	}
	// Upstream pi.exec resolves a program that cannot start as code 1.
	if m := res.Details.MissingExec; m == nil || m.Code != 1 || m.Stdout != "" || m.Stderr != "" || m.Killed {
		t.Errorf("missing-program exec = %+v, want {code:1}", m)
	}
	if res.Details.ContextUsage.Tokens != 42 || res.Details.ContextUsage.ContextWindow != 1000 {
		t.Errorf("contextUsage = %+v, want {Tokens:42 ContextWindow:1000}", res.Details.ContextUsage)
	}

	cmd, ok := ext.Commands["ping_ts"]
	if !ok {
		t.Fatal("missing ping_ts command")
	}
	if err := cmd.Handler(ctx, ""); err != nil {
		t.Fatalf("ping_ts: %v", err)
	}
	pollUntil(t, testTimeout(t, 2*time.Second), "notifications never contained pong-ts", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		return len(notifications) > 0
	})
	notifyMu.Lock()
	lastNotify := ""
	if len(notifications) > 0 {
		lastNotify = notifications[len(notifications)-1]
	}
	gotNotifications := append([]string(nil), notifications...)
	notifyMu.Unlock()
	if lastNotify != "pong-ts" {
		t.Fatalf("notifications = %v, want pong-ts", gotNotifications)
	}

	if len(ext.Handlers["session_start"]) == 0 {
		t.Fatal("session_start handler missing from TS shim register payload")
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
		t.Fatalf("dispatch session_start: %v", err)
	}
	pollUntil(t, testTimeout(t, 2*time.Second), "header/footer/editor never set", func() bool {
		s := fakeUI.uiSnapshot()
		return len(s.headerLines) > 0 && len(s.footerLines) > 0 && s.editorLabel != ""
	})
	uiState := fakeUI.uiSnapshot()
	if len(uiState.headerLines) == 0 || !strings.Contains(uiState.headerLines[0], "ts-header") {
		t.Fatalf("headerLines = %v, want ts-header", uiState.headerLines)
	}
	if len(uiState.footerLines) == 0 || !strings.Contains(uiState.footerLines[0], "ts-footer") {
		t.Fatalf("footerLines = %v, want ts-footer", uiState.footerLines)
	}
	// setEditorComponent decoration must reach the host with the
	// captured label + ANSI prefix + history.
	if uiState.editorLabel != "ts-mode" {
		t.Errorf("editorLabel = %q, want ts-mode", uiState.editorLabel)
	}
	if !strings.Contains(uiState.editorBorderPrefix, "\x1b[31m") {
		t.Errorf("editorBorderPrefix = %q, want SGR red prefix", uiState.editorBorderPrefix)
	}
	if len(uiState.editorHistory) != 2 || uiState.editorHistory[0] != "history-1" || uiState.editorHistory[1] != "history-2" {
		t.Errorf("editorHistory = %v, want [history-1 history-2]", uiState.editorHistory)
	}

	// Wait for the autocomplete provider to register, then drive it
	// directly through the captured AsyncSuggestionSource so we
	// exercise the full Go→Node→handler→response round trip.
	pollUntil(t, testTimeout(t, 5*time.Second), "autocompleteSource was not registered", func() bool {
		return fakeUI.autocompleteProvider() != nil
	})
	autocomplete := fakeUI.autocompleteProvider()
	suggestCtx, suggestCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer suggestCancel()
	got := autocomplete.Suggest(suggestCtx, []string{"check #5"}, 0, len("check #5"))
	if got == nil {
		t.Fatal("autocomplete returned nil for #5 prefix")
		return
	}
	if got.Prefix != "#5" {
		t.Errorf("autocomplete prefix = %q, want #5", got.Prefix)
	}
	if len(got.Items) != 2 || got.Items[0].Value != "#1" || got.Items[1].Value != "#2" {
		t.Errorf("autocomplete items = %v, want [#1 #2]", got.Items)
	}
	// And confirm the chain falls back gracefully when no token is
	// present (base provider returns null → suggestions are nil).
	got2 := autocomplete.Suggest(suggestCtx, []string{"plain text"}, 0, len("plain text"))
	if got2 != nil {
		t.Errorf("autocomplete on plain text = %+v, want nil", got2)
	}
}

func TestHost_Integration_TSWidgetFactoryRendersInvalidatesResizesAndClears(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	shortSockDir(t)
	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "widget-factory",
		Source:  filepath.Join("testdata", "widget-factory.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bridge.SetUIContext(&themedTestUI{UIContext: extension.NoopUIContext, theme: map[string]any{
		"foregrounds": map[string]string{"accent": "\x1b[31m"},
		"backgrounds": map[string]string{},
	}})
	// Frames carry the width they were rendered at; render at that width.
	h.NotifyWidth(80)
	if err := ext.Commands["widget_factory"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 5*time.Second, "widget factory did not publish invalidated frame", func() bool {
		proxy := bridge.GetWidget("widget-factory", "dynamic")
		if proxy == nil {
			return false
		}
		lines := proxy.Render(80)
		return len(lines) == 2 && strings.Contains(lines[0], "widget value=1") && strings.TrimSpace(lines[1]) == "widget second line"
	})
	h.NotifyWidth(91)
	pollUntil(t, 5*time.Second, "widget factory did not rerender after resize", func() bool {
		proxy := bridge.GetWidget("widget-factory", "dynamic")
		if proxy == nil {
			return false
		}
		lines := proxy.Render(91)
		return len(lines) == 2 && strings.Contains(lines[0], "widget value=1 width=91") && strings.Contains(lines[0], "\x1b[1m") && strings.TrimSpace(lines[1]) == "widget second line"
	})
	if err := ext.Commands["widget_clear"].Handler(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, 5*time.Second, "widget factory did not clear", func() bool {
		return bridge.GetWidget("widget-factory", "dynamic") == nil
	})
}

type themedTestUI struct {
	extension.UIContext
	theme extension.Theme
}

func (u *themedTestUI) Theme() extension.Theme { return u.theme }

// TestHost_Integration_TSCustomOverlay drives a real ctx.ui.custom round-trip
// through the TS subprocess shim: the host opens a remote overlay, the TS
// component renders frames into it, host-side input chunks are forwarded back
// to Node, the component aggregates them, and done(value) closes the overlay
// with that value.
func TestHost_Integration_TSCustomOverlay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	modRoot := findModuleRoot(t)
	shortSockDir(t)

	var notifications []string
	var notifyMu sync.Mutex
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}

	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{
		IsIdle: func() bool { return true },
	})

	h := newTestHost(t)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tsFile := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "testdata", "ts-fixture.ts")
	_ = tsFile
	ext, err := h.Load(loadCtx, ExtConfig{
		Name:    "ts-fixture-overlay",
		Source:  filepath.Join("testdata", "ts-fixture.ts"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load TS fixture: %v", err)
	}
	_ = modRoot

	cmd, ok := ext.Commands["custom_ts"]
	if !ok {
		t.Fatal("ts fixture missing custom_ts command")
	}

	// Drive the command in a goroutine so we can observe the overlay
	// state in this goroutine. The command synchronously awaits the
	// "ui.custom" host call inside the subprocess; on the host side
	// our testUIContext.RunRemoteOverlay blocks until we signal close.
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Handler(context.Background(), "")
	}()

	handle, host := fakeUI.awaitOverlay(t, 10*time.Second)
	if handle == nil || host == nil {
		t.Fatal("overlay open did not surface handle/host")
	}

	// Wait for the initial render frame to arrive from Node so we know
	// the component is wired up before we start pushing input. TS overlay
	// rendering includes Node startup and cross-process invalidation, so
	// give it a larger deterministic budget than pure Go IPC tests.
	tsOverlayTimeout := testTimeout(t, 15*time.Second)
	pollUntil(t, tsOverlayTimeout, "no initial render frame from ts overlay component", func() bool {
		return len(handle.Lines()) > 0
	})

	// Feed input: type "hi" then press Enter to close with value.
	host.OnInput("h")
	host.OnInput("i")
	// Wait for the buffered render frame to surface "hi" so we know the
	// component has processed the keystrokes before submission.
	pollUntil(t, tsOverlayTimeout, "render frame never surfaced 'hi'", func() bool {
		lines := handle.Lines()
		return len(lines) > 0 && strings.Contains(lines[0], "hi")
	})
	host.OnInput("\r")

	select {
	case <-handle.Done():
	case <-time.After(testTimeout(t, 10*time.Second)):
		t.Fatal("overlay did not close after Enter")
	}

	if err := <-cmdDone; err != nil {
		t.Fatalf("command handler error: %v", err)
	}

	// Wait for the trailing notify("custom-ts-result:hi") from the
	// fixture to land via the bridge.
	pollUntil(t, testTimeout(t, 5*time.Second), "missing custom-ts-result notify", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		for _, n := range notifications {
			if strings.Contains(n, "custom-ts-result:hi") {
				return true
			}
		}
		return false
	})
}

// TestHost_Integration_TSDoomOverlayOptions pins the Node runtime half of
// upstream ui.custom({overlay: true, overlayOptions}) for the doom-overlay
// option set: the options cross the bridge intact (no legacy modal fields) and
// the component renders at upstream's resolved overlay width (75% of the
// terminal), not at the full terminal width.
func TestHost_Integration_TSDoomOverlayOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	shortSockDir(t)
	fakeUI := newTestUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	// The bridge drops frames laid out for a stale terminal width; the real
	// host sets this, so a frame tagged with the render width would vanish.
	bridge.SetWidth(120)
	bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})
	h := newTestHost(t)
	h.SetWidthFunc(func() int { return 120 })
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")
	loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ext, err := h.Load(loadCtx, ExtConfig{Name: "ts-fixture-doom", Source: filepath.Join("testdata", "ts-fixture.ts"), Enabled: true})
	if err != nil {
		t.Fatalf("Load TS fixture: %v", err)
	}
	cmd, ok := ext.Commands["doom_overlay_ts"]
	if !ok {
		t.Fatal("ts fixture missing doom_overlay_ts command")
	}
	cmdDone := make(chan error, 1)
	go func() { cmdDone <- cmd.Handler(context.Background(), "") }()
	handle, host := fakeUI.awaitOverlay(t, 10*time.Second)
	fakeUI.overlayMu.Lock()
	opts := fakeUI.overlayOpts
	fakeUI.overlayMu.Unlock()
	if !opts.Overlay || opts.Title != "" || opts.WidthFraction != 0 || opts.HeightFraction != 0 {
		t.Fatalf("overlay opts = %+v, want upstream overlay mode without legacy modal fields", opts)
	}
	l := opts.Layout
	if l == nil || l.Width == nil || *l.Width != (extension.OverlaySizeValue{Value: 75, Percent: true}) ||
		l.MaxHeight == nil || *l.MaxHeight != (extension.OverlaySizeValue{Value: 95, Percent: true}) ||
		l.Anchor != "center" || l.Margin == nil || l.Margin.All != nil || l.Margin.Top != 1 {
		t.Fatalf("overlayOptions did not cross the bridge intact: %+v", l)
	}
	timeout := testTimeout(t, 15*time.Second)
	pollUntil(t, timeout, "no render frame from doom-shaped overlay", func() bool { return len(handle.Lines()) > 0 })
	lines := handle.Lines()
	// 75% of 120 = 90 columns; height floor(90/3.2)=28 rows plus the HUD.
	if len(lines) != 29 || len(lines[0]) != 90 || !strings.HasPrefix(lines[28], "HUD w=90") {
		t.Fatalf("frame = %d lines, first width %d, last %q; want 29 lines rendered at width 90", len(lines), len(lines[0]), lines[len(lines)-1])
	}
	host.OnInput("q")
	select {
	case <-handle.Done():
	case <-time.After(testTimeout(t, 10*time.Second)):
		t.Fatal("overlay did not close on q")
	}
	if err := <-cmdDone; err != nil {
		t.Fatalf("command handler error: %v", err)
	}
}

// TestHost_Integration_SDKAgentControlMethods exercises agent-control wrappers.
func TestHost_Integration_SDKAgentControlMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildSDKFixture(t)
	shortSockDir(t)

	var notifications []string
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, level string) {
		notifications = append(notifications, msg)
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{
		IsIdle:             func() bool { return true },
		HasPendingMessages: func() bool { return false },
		Compact:            func(context.Context, *extension.CompactOptions) { /* no-op */ },
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "sdk-fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd, ok := ext.Commands["agent-probe"]
	if !ok {
		t.Fatal("SDK extension missing agent-probe command")
	}
	if err := cmd.Handler(context.Background(), ""); err != nil {
		t.Fatalf("agent-probe: %v", err)
	}

	if len(notifications) == 0 {
		t.Fatal("expected summary notification from agent-probe")
	}
	var summary struct {
		Idle    bool `json:"idle"`
		Pending bool `json:"pending"`
	}
	if err := json.Unmarshal([]byte(notifications[len(notifications)-1]), &summary); err != nil {
		t.Fatalf("summary json: %v\nraw=%q", err, notifications[len(notifications)-1])
	}
	if !summary.Idle {
		t.Fatalf("idle = %v, want true", summary.Idle)
	}
	if summary.Pending {
		t.Fatalf("pending = %v, want false", summary.Pending)
	}
}

// TestHost_Integration_SDKSessionControlMethods exercises session-control wrappers.
func TestHost_Integration_SDKSessionControlMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	binPath := buildSDKFixture(t)
	shortSockDir(t)

	var notifications []string
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, level string) {
		notifications = append(notifications, msg)
	}

	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{
		WaitForIdle: func(context.Context) error { return nil },
		Reload:      func(context.Context) error { return nil },
	})
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ext, err := h.Load(ctx, ExtConfig{Name: "sdk-fixture", Path: binPath, Enabled: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cmd, ok := ext.Commands["session-probe"]
	if !ok {
		t.Fatal("SDK extension missing session-probe command")
	}
	if err := cmd.Handler(context.Background(), ""); err != nil {
		t.Fatalf("session-probe: %v", err)
	}

	if len(notifications) == 0 {
		t.Fatal("expected summary notification from session-probe")
	}
	var summary struct {
		WaitErr   string `json:"waitErr"`
		ReloadErr string `json:"reloadErr"`
	}
	if err := json.Unmarshal([]byte(notifications[len(notifications)-1]), &summary); err != nil {
		t.Fatalf("summary json: %v\nraw=%q", err, notifications[len(notifications)-1])
	}
	if summary.WaitErr != "" {
		t.Fatalf("waitErr = %q", summary.WaitErr)
	}
	if summary.ReloadErr != "" {
		t.Fatalf("reloadErr = %q", summary.ReloadErr)
	}
}

// TestHost_Integration_NoExtensionsNilSafe proves that NewHost + no extensions
// loaded does not panic. Catches the typed-nil interface trap.
func TestHost_Integration_NoExtensionsNilSafe(t *testing.T) {
	h := newTestHost(t)

	// Extensions() on empty host must return empty, not panic.
	exts := h.Extensions()
	if len(exts) != 0 {
		t.Errorf("empty host has %d extensions, want 0", len(exts))
	}

	// Shutdown on empty host must not panic.
	h.SetConfigLoader(func() ([]ExtConfig, error) { return nil, nil })
	h.Shutdown("test")

	// Reload on empty host with no config must not panic.
	t.Setenv("HOME", t.TempDir())
	reloaded, err := h.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(reloaded) != 0 {
		t.Errorf("empty reload has %d extensions, want 0", len(reloaded))
	}
}

// TestHost_Integration_RequestRenderFromTimer proves that the tui shim's
// requestRender method triggers frame pushes independently of handleInput.
// The timer-overlay.mjs extension sets a 50ms interval that increments a
// counter and calls tui.requestRender(); no host.OnInput() is called here.
// If requestRender doesn't work (e.g. the shim is {}), frames never advance
// and the test times out.
func TestHost_Integration_RequestRenderFromTimer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	shortSockDir(t)

	var notifications []string
	var notifyMu sync.Mutex
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}

	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{
		IsIdle: func() bool { return true },
	})

	h := newTestHost(t)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ext, err := h.Load(loadCtx, ExtConfig{
		Name:    "timer-overlay",
		Source:  filepath.Join("testdata", "timer-overlay.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load timer-overlay: %v", err)
	}

	cmd, ok := ext.Commands["timer_overlay"]
	if !ok {
		t.Fatal("timer-overlay missing timer_overlay command")
	}

	// Drive the command
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Handler(context.Background(), "")
	}()

	handle, _ := fakeUI.awaitOverlay(t, 10*time.Second)
	if handle == nil {
		t.Fatal("overlay open did not surface handle")
	}

	// Wait for frame advancement WITHOUT sending any input.
	// If requestRender works, the extension's setInterval pushes frames
	// to the host which updates handle.Lines().
	pollUntil(t, testTimeout(t, 10*time.Second), "frame never advanced past 0: requestRender not working", func() bool {
		lines := handle.Lines()
		if len(lines) == 0 {
			return false
		}
		// Frame should reach at least 2 (timer fires at 50ms intervals)
		return strings.Contains(lines[0], "frame=2") || strings.Contains(lines[0], "frame=3")
	})

	// The overlay auto-closes after 3 frames
	select {
	case <-handle.Done():
	case <-time.After(testTimeout(t, 10*time.Second)):
		t.Fatal("overlay did not auto-close after 3 timer frames")
	}

	if err := <-cmdDone; err != nil {
		t.Fatalf("command handler error: %v", err)
	}

	// Verify the notify fired with shim type confirmation
	pollUntil(t, testTimeout(t, 5*time.Second), "missing timer-result notify", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		for _, n := range notifications {
			if strings.Contains(n, `timer-result:`) && strings.Contains(n, `"shimType":"function"`) && strings.Contains(n, "disposed=true") {
				return true
			}
		}
		return false
	})

	// Final assertion: the notify must confirm shimType was "function"
	notifyMu.Lock()
	found := false
	for _, n := range notifications {
		if strings.Contains(n, `"shimType":"function"`) {
			found = true
			break
		}
	}
	notifyMu.Unlock()
	if !found {
		t.Error("tui.requestRender was not typeof 'function': shim fix not applied")
	}
	pollUntil(t, testTimeout(t, 5*time.Second), "timer overlay remained registered after disposal", func() bool {
		bridge.mu.RLock()
		defer bridge.mu.RUnlock()
		return len(bridge.customOverlays) == 0
	})
}

func TestHost_Integration_RequestRenderBurstIsCoalescedBeforeIPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	shortSockDir(t)

	var notifications []string
	var notifyMu sync.Mutex
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(message, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, message)
		notifyMu.Unlock()
	}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	host := newTestHost(t)
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")

	ext, err := host.Load(context.Background(), ExtConfig{
		Name:    "burst-overlay",
		Source:  filepath.Join("testdata", "burst-overlay.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := ext.Commands["burst_overlay"]
	if command.Handler == nil {
		t.Fatal("burst-overlay missing burst_overlay command")
	}
	commandDone := make(chan error, 1)
	go func() { commandDone <- command.Handler(context.Background(), "") }()
	handle, _ := fakeUI.awaitOverlay(t, 10*time.Second)
	select {
	case <-handle.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("burst overlay did not close")
	}
	if err := <-commandDone; err != nil {
		t.Fatal(err)
	}
	if updates := handle.UpdateCount(); updates > 4 {
		t.Fatalf("10,000 replaceable redraw requests produced %d IPC frames, want at most 4", updates)
	}
	pollUntil(t, 5*time.Second, "missing burst result", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		return len(notifications) > 0
	})
	var renders int
	notifyMu.Lock()
	for _, message := range notifications {
		_, _ = fmt.Sscanf(message, `burst-result:{"renders":%d}`, &renders)
	}
	notifyMu.Unlock()
	if renders == 0 || renders > 4 {
		t.Fatalf("component render count = %d, want 1..4 after 10,000 requests", renders)
	}
}

func TestHost_Integration_TSParameterProperties(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	shortSockDir(t)

	var notifications []string
	var notifyMu sync.Mutex
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}

	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})

	h := newTestHost(t)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ext, err := h.Load(loadCtx, ExtConfig{
		Name:    "ts-param-prop",
		Source:  filepath.Join("testdata", "ts-param-prop.ts"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load ts-param-prop fixture: %v", err)
	}

	cmd, ok := ext.Commands["param_prop_ts"]
	if !ok {
		t.Fatal("ts-param-prop fixture missing param_prop_ts command")
	}
	if err := cmd.Handler(context.Background(), ""); err != nil {
		t.Fatalf("param_prop_ts handler: %v", err)
	}

	pollUntil(t, testTimeout(t, 5*time.Second), "missing param-prop notify", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		for _, n := range notifications {
			if strings.Contains(n, "param-prop:loaded") {
				return true
			}
		}
		return false
	})
}

// Upstream awaits extension tool execution and commands with no completion
// deadline. A focused custom overlay therefore remains active until the user
// closes it or the caller cancels the request.
func TestHost_Integration_ToolCustomOverlayWaitsForUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	shortSockDir(t)

	fakeUI := newTestUIContext()

	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})

	h := newTestHost(t)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ext, err := h.Load(loadCtx, ExtConfig{
		Name:    "linger-overlay-tool",
		Source:  filepath.Join("testdata", "linger-overlay-tool.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load linger-overlay-tool fixture: %v", err)
	}

	tool, ok := ext.Tools["linger_overlay_tool"]
	if !ok {
		t.Fatal("linger-overlay-tool fixture missing linger_overlay_tool tool")
	}

	type toolOutcome struct {
		result extension.AgentToolResult
		err    error
	}
	toolDone := make(chan toolOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := tool.Definition.Execute(ctx, "tc_linger", json.RawMessage(`{}`), nil)
		toolDone <- toolOutcome{result: res, err: err}
	}()

	handle, host := fakeUI.awaitOverlay(t, 10*time.Second)
	if handle == nil || host == nil {
		t.Fatal("overlay open did not surface handle/host")
	}

	select {
	case outcome := <-toolDone:
		t.Fatalf("tool returned before the user closed its overlay: %+v", outcome)
	default:
	}
	host.OnInput("q")

	select {
	case <-handle.Done():
	case <-time.After(testTimeout(t, 5*time.Second)):
		t.Fatal("linger overlay did not close after q")
	}

	outcome := <-toolDone
	if outcome.err != nil {
		t.Fatalf("linger_overlay_tool execute error: %v", outcome.err)
	}
	if outcome.result == nil {
		t.Fatal("linger_overlay_tool returned no result")
	}
}

func TestHost_Integration_CommandCustomOverlayWaitsForUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	shortSockDir(t)

	var notifications []string
	var notifyMu sync.Mutex
	fakeUI := newTestUIContext()
	fakeUI.onNotify = func(msg, _ string) {
		notifyMu.Lock()
		notifications = append(notifications, msg)
		notifyMu.Unlock()
	}

	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})

	h := newTestHost(t)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	loadCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ext, err := h.Load(loadCtx, ExtConfig{
		Name:    "linger-overlay",
		Source:  filepath.Join("testdata", "linger-overlay.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Load linger-overlay fixture: %v", err)
	}

	cmd, ok := ext.Commands["linger_overlay"]
	if !ok {
		t.Fatal("linger-overlay fixture missing linger_overlay command")
	}

	cmdDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmdDone <- cmd.Handler(ctx, "")
	}()

	handle, host := fakeUI.awaitOverlay(t, 10*time.Second)
	if handle == nil || host == nil {
		t.Fatal("overlay open did not surface handle/host")
	}

	select {
	case err := <-cmdDone:
		t.Fatalf("command returned before the user closed its overlay: %v", err)
	default:
	}
	host.OnInput("q")

	select {
	case <-handle.Done():
	case <-time.After(testTimeout(t, 5*time.Second)):
		t.Fatal("linger overlay did not close after q")
	}

	if err := <-cmdDone; err != nil {
		t.Fatalf("linger_overlay handler error: %v", err)
	}

	pollUntil(t, testTimeout(t, 5*time.Second), "missing linger-result notify", func() bool {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		for _, n := range notifications {
			if strings.Contains(n, "linger-result:closed") {
				return true
			}
		}
		return false
	})
}
