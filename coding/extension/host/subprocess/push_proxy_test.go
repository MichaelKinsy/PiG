package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestPushProxy_RenderReturnsCache(t *testing.T) {
	var invalidated atomic.Int32
	proxy := NewPushProxy(func() { invalidated.Add(1) }, nil)

	// Empty initially.
	lines := proxy.Render(80)
	if lines != nil {
		t.Errorf("empty proxy rendered %v", lines)
	}

	// Push some lines.
	proxy.UpdateLines([]string{"line 1", "line 2"})

	// Should have triggered invalidate.
	if invalidated.Load() != 1 {
		t.Errorf("invalidated = %d, want 1", invalidated.Load())
	}

	// Render returns the cached lines.
	lines = proxy.Render(80)
	if len(lines) != 2 || lines[0] != "line 1" || lines[1] != "line 2" {
		t.Errorf("Render = %v, want [line 1, line 2]", lines)
	}
}

func TestPushProxy_RenderNeverBlocks(t *testing.T) {
	proxy := NewPushProxy(func() {}, nil)
	proxy.UpdateLines([]string{"cached"})

	// Render must complete in <1ms (it's a cache read).
	start := time.Now()
	for range 10000 {
		_ = proxy.Render(120)
	}
	elapsed := time.Since(start)

	// 10000 renders should take well under 100ms.
	if elapsed > 100*time.Millisecond {
		t.Errorf("10000 renders took %v (too slow for a cache read)", elapsed)
	}
}

func TestPushProxy_ReturnsCopy(t *testing.T) {
	proxy := NewPushProxy(func() {}, nil)
	proxy.UpdateLines([]string{"a", "b"})

	lines := proxy.Render(80)
	lines[0] = "mutated"

	// Original cache should be unchanged.
	cached := proxy.Lines()
	if cached[0] != "a" {
		t.Errorf("cache was mutated: %v", cached)
	}
}

func TestPushProxy_Clear(t *testing.T) {
	var invalidated atomic.Int32
	proxy := NewPushProxy(func() { invalidated.Add(1) }, nil)
	proxy.UpdateLines([]string{"hello"})

	proxy.Clear()

	if invalidated.Load() != 2 { // one for update, one for clear
		t.Errorf("invalidated = %d, want 2", invalidated.Load())
	}

	lines := proxy.Render(80)
	if lines != nil {
		t.Errorf("after Clear, Render = %v", lines)
	}
}

func TestPushProxy_WidthChange(t *testing.T) {
	var notifiedWidth atomic.Int32
	proxy := NewPushProxy(func() {}, func(w int) {
		notifiedWidth.Store(int32(w))
	})
	proxy.UpdateLines([]string{"x"})

	// First render reports the actual TUI width so extensions that rendered
	// from a stale ready payload can refresh to the real terminal width.
	_ = proxy.Render(80)
	time.Sleep(50 * time.Millisecond) // notification is async
	if notifiedWidth.Load() != 80 {
		t.Errorf("first render notification = %d, want 80", notifiedWidth.Load())
	}

	// Width change triggers notification.
	_ = proxy.Render(120)
	time.Sleep(50 * time.Millisecond) // notification is async
	if notifiedWidth.Load() != 120 {
		t.Errorf("width notification = %d, want 120", notifiedWidth.Load())
	}
}

// ── UIBridge tests ───────────────────────────────────────────────────────────

// fakeUIContext implements extension.UIContext for UIBridge tests by embedding
// NoopUIContext and overriding the methods we need to verify.
type fakeUIContext struct {
	extension.UIContext // embed NoopUIContext for all 25 methods
	notifyCalls         []string
	statusCalls         []string
	selectResult        string
	selectOk            bool
	selectErr           error
	confirmResult       bool
	inputResult         string
	inputOk             bool
	inputErr            error
	editorResult        string
	editorErr           error
}

func newFakeUIContext() *fakeUIContext {
	return &fakeUIContext{UIContext: extension.NoopUIContext}
}

func (f *fakeUIContext) Notify(msg, level string) {
	f.notifyCalls = append(f.notifyCalls, msg+":"+level)
}
func (f *fakeUIContext) SetStatus(key, text string) {
	f.statusCalls = append(f.statusCalls, key+"="+text)
}
func (f *fakeUIContext) Select(_ context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if f.selectErr != nil {
		return "", f.selectErr
	}
	if !f.selectOk {
		return "", context.Canceled
	}
	return f.selectResult, nil
}
func (f *fakeUIContext) Confirm(_ context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	return f.confirmResult, nil
}
func (f *fakeUIContext) Input(_ context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if f.inputErr != nil {
		return "", f.inputErr
	}
	if !f.inputOk {
		return "", context.Canceled
	}
	return f.inputResult, nil
}
func (f *fakeUIContext) Editor(_ context.Context, _, _ string) (string, error) {
	if f.editorErr != nil {
		return "", f.editorErr
	}
	return f.editorResult, nil
}

func TestUIBridge_HandleNotify(t *testing.T) {
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	args, _ := json.Marshal(map[string]string{"message": "hello", "level": "info"})
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.notify",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if len(fake.notifyCalls) != 1 || fake.notifyCalls[0] != "hello:info" {
		t.Errorf("notify calls = %v", fake.notifyCalls)
	}
}

func TestUIBridge_HandleSetStatus(t *testing.T) {
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	args, _ := json.Marshal(map[string]string{"key": "subagent", "text": "3 running"})
	_, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.setStatus",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if len(fake.statusCalls) != 1 || fake.statusCalls[0] != "subagent=3 running" {
		t.Errorf("status calls = %v", fake.statusCalls)
	}
}

func TestUIBridge_HandleSelect(t *testing.T) {
	fake := newFakeUIContext()
	fake.selectResult = "option2"
	fake.selectOk = true
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	args, _ := json.Marshal(map[string]any{"title": "Pick", "options": []string{"option1", "option2"}})
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.select",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}

	var resp struct {
		Selected string `json:"selected"`
		Ok       bool   `json:"ok"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	if resp.Selected != "option2" || !resp.Ok {
		t.Errorf("select result = %+v", resp)
	}
}

func TestUIBridge_DialogCancellationAndFailureAreDistinct(t *testing.T) {
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)
	args, _ := json.Marshal(map[string]any{"title": "Pick", "options": []string{"option1"}})

	result, err := bridge.HandleCall("ext1", &CallPayload{Method: "ui.select", Args: args})
	if err != nil || result.Error != nil {
		t.Fatalf("cancel result = %+v, %v", result, err)
	}
	var cancelled struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal(result.Result, &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Ok {
		t.Fatal("cancelled select reported success")
	}

	fake.selectErr = errors.New("dialog transport failed")
	result, err = bridge.HandleCall("ext1", &CallPayload{Method: "ui.select", Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == nil || result.Error.Code != "ui_error" || result.Error.Message != "dialog transport failed" {
		t.Fatalf("failed select result = %+v", result)
	}
}

func TestUIBridge_EmptyDialogSubmissionIsSuccessful(t *testing.T) {
	fake := newFakeUIContext()
	fake.inputOk = true
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)
	args, _ := json.Marshal(map[string]any{"title": "Optional", "placeholder": ""})

	result, err := bridge.HandleCall("ext1", &CallPayload{Method: "ui.input", Args: args})
	if err != nil || result.Error != nil {
		t.Fatalf("input result = %+v, %v", result, err)
	}
	var submitted struct {
		Text string `json:"text"`
		Ok   bool   `json:"ok"`
	}
	if err := json.Unmarshal(result.Result, &submitted); err != nil {
		t.Fatal(err)
	}
	if !submitted.Ok || submitted.Text != "" {
		t.Fatalf("empty input result = %+v", submitted)
	}
}

func TestUIBridge_HeadlessDialogReturnsFallback(t *testing.T) {
	bridge := NewUIBridge(func() {})
	args, _ := json.Marshal(map[string]any{"title": "Pick", "options": []string{"option1"}})
	result, err := bridge.HandleCall("ext1", &CallPayload{Method: "ui.select", Args: args})
	if err != nil || result.Error != nil {
		t.Fatalf("headless result = %+v, %v", result, err)
	}
	var fallback struct {
		Ok bool `json:"ok"`
	}
	if err := json.Unmarshal(result.Result, &fallback); err != nil {
		t.Fatal(err)
	}
	if fallback.Ok {
		t.Fatal("headless select reported an interactive result")
	}
}

func TestUIBridge_HandleWidgetPush(t *testing.T) {
	var invalidated atomic.Int32
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() { invalidated.Add(1) })
	bridge.SetUIContext(fake)

	bridge.HandleWidgetPush("ext1", &WidgetPushPayload{
		Key:   "status",
		Lines: []string{"◆ 3 files, 42 symbols indexed"},
	})

	proxy := bridge.GetWidget("ext1", "status")
	if proxy == nil {
		t.Fatal("widget proxy is nil")
	}

	lines := proxy.Render(80)
	if len(lines) != 1 || lines[0] != "◆ 3 files, 42 symbols indexed" {
		t.Errorf("widget lines = %v", lines)
	}

	if invalidated.Load() != 1 {
		t.Errorf("invalidated = %d, want 1", invalidated.Load())
	}
}

func TestUIBridge_HandleSetWidget_Clear(t *testing.T) {
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	// Set a widget.
	bridge.HandleWidgetPush("ext1", &WidgetPushPayload{
		Key:   "counter",
		Lines: []string{"count: 5"},
	})

	// Clear it via setWidget with nil lines.
	args, _ := json.Marshal(map[string]any{"key": "counter", "lines": nil})
	_, _ = bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.setWidget",
		Args:   args,
	})

	proxy := bridge.GetWidget("ext1", "counter")
	if proxy != nil {
		t.Error("widget should be nil after clear")
	}
}

func TestUIBridge_ClearExtension(t *testing.T) {
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	bridge.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "a", Lines: []string{"1"}})
	bridge.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "b", Lines: []string{"2"}})
	bridge.HandleWidgetPush("ext2", &WidgetPushPayload{Key: "c", Lines: []string{"3"}})

	bridge.ClearExtension("ext1")

	if bridge.GetWidget("ext1", "a") != nil {
		t.Error("ext1:a should be cleared")
	}
	if bridge.GetWidget("ext1", "b") != nil {
		t.Error("ext1:b should be cleared")
	}
	if bridge.GetWidget("ext2", "c") == nil {
		t.Error("ext2:c should NOT be cleared")
	}
}

func TestUIBridge_UnknownMethod(t *testing.T) {
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.nonexistent",
		Args:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if result.Error.Code != "unknown_method" {
		t.Errorf("error code = %q", result.Error.Code)
	}
}

// ── Tests for new bridged methods ────────────────────────────────────────────

func TestUIBridge_HandleSendMessage(t *testing.T) {
	bridge := NewUIBridge(func() {})

	var sent extension.CustomMessageRef
	var sentOpts SendMessageOptions
	bridge.SetActions(&HostCallbacks{
		SendMessage: func(msg extension.CustomMessageRef, opts SendMessageOptions) error {
			sent = msg
			sentOpts = opts
			return nil
		},
	})

	args, _ := json.Marshal(map[string]any{
		"message": map[string]any{
			"customType": "subagent-completion",
			"content":    "Agent finished",
			"display":    true,
		},
		"options": map[string]any{
			"triggerTurn": true,
			"deliverAs":   "followUp",
		},
	})
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "sendMessage",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %+v", result.Error)
	}
	if sent.CustomType != "subagent-completion" {
		t.Errorf("customType = %q, want subagent-completion", sent.CustomType)
	}
	if sentOpts.TriggerTurn == nil || !*sentOpts.TriggerTurn {
		t.Error("triggerTurn should be true")
	}
	if sentOpts.DeliverAs != "followUp" {
		t.Errorf("deliverAs = %q, want followUp", sentOpts.DeliverAs)
	}
}

func TestUIBridge_HandleSendMessage_NotReady(t *testing.T) {
	bridge := NewUIBridge(func() {})
	// No actions set: should return "not_ready" error.

	args, _ := json.Marshal(map[string]any{
		"message": map[string]any{"customType": "test", "content": "hi"},
	})
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "sendMessage",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if result.Error == nil || result.Error.Code != "not_ready" {
		t.Errorf("expected not_ready error, got %+v", result.Error)
	}
}

func TestUIBridge_HandleGetSessionName(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{
		GetSessionName: func() string { return "my-session" },
	})

	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "getSessionName",
		Args:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	var resp struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	if resp.Name != "my-session" {
		t.Errorf("name = %q, want my-session", resp.Name)
	}
}

func TestUIBridge_HandleSetThinkingLevel(t *testing.T) {
	bridge := NewUIBridge(func() {})
	var setLevel string
	bridge.SetActions(&HostCallbacks{
		SetThinkingLevel: func(level string) { setLevel = level },
	})

	args, _ := json.Marshal(map[string]string{"level": "high"})
	_, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "setThinkingLevel",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if setLevel != "high" {
		t.Errorf("level = %q, want high", setLevel)
	}
}

func TestUIBridge_HandleSetTitle(t *testing.T) {
	// Uses the UIContext path: verify it calls through to the real context.
	fake := newFakeUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fake)

	// SetTitle is on NoopUIContext (no-op), but verifying the dispatch works.
	args, _ := json.Marshal(map[string]string{"title": "My Agent"})
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.setTitle",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %+v", result.Error)
	}
}

func TestUIBridge_HandleGetEditorText(t *testing.T) {
	// NoopUIContext.GetEditorText() returns ""
	bridge := NewUIBridge(func() {})

	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.getEditorText",
		Args:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	var resp struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	if resp.Text != "" {
		t.Errorf("text = %q, want empty", resp.Text)
	}
}

func TestUIBridge_HandleSetToolsExpanded(t *testing.T) {
	bridge := NewUIBridge(func() {})

	args, _ := json.Marshal(map[string]bool{"expanded": true})
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.setToolsExpanded",
		Args:   args,
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %+v", result.Error)
	}
}

func TestUIBridge_HandleUnsupported(t *testing.T) {
	bridge := NewUIBridge(func() {})

	// ui.custom is supported for the TS shim runtime (which supplies a
	// unique key plus a renderable factory). SDK Go/Rust callers that
	// omit the key still get the upstream-incompatible "unsupported"
	// sentinel because factory closures cannot cross the socket.
	result, err := bridge.HandleCall("ext1", &CallPayload{
		Method: "ui.custom",
		Args:   json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("HandleCall: %v", err)
	}
	if result.Error == nil || result.Error.Code != "unsupported" {
		t.Errorf("expected unsupported error, got %+v", result.Error)
	}
}
