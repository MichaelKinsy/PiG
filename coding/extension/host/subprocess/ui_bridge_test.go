package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// mockUIContext records calls for verification.
type mockUIContext struct {
	notifyCalls        []struct{ msg, kind string }
	statusCalls        []struct{ key, text string }
	titleCalls         []string
	workingMsgCalls    []string
	thinkingLabelCalls []string
	pasteToEditorCalls []string
	setEditorTextCalls []string
	editorText         string
	toolsExpanded      bool
	footerCleared      bool
	footerValue        any
	headerCleared      bool
	headerCalls        []any
	loginCalls         []extension.LoginDefinition
	loginErr           error
	editorCompCleared  bool
	workingIndCalls    []any

	selectResult  string
	selectErr     error
	confirmResult bool
	confirmErr    error
	inputResult   string
	inputErr      error
	editorResult  string
	editorErr     error
	themeResult   extension.SetThemeResult
	theme         extension.Theme
	namedTheme    extension.Theme
	allThemes     []extension.ThemeMeta
}

func (m *mockUIContext) Notify(msg, kind string) {
	m.notifyCalls = append(m.notifyCalls, struct{ msg, kind string }{msg, kind})
}
func (m *mockUIContext) SetStatus(key, text string) {
	m.statusCalls = append(m.statusCalls, struct{ key, text string }{key, text})
}
func (m *mockUIContext) SetTitle(title string) { m.titleCalls = append(m.titleCalls, title) }
func (m *mockUIContext) SetWorkingMessage(msg string) {
	m.workingMsgCalls = append(m.workingMsgCalls, msg)
}
func (m *mockUIContext) SetWorkingVisible(bool) {}
func (m *mockUIContext) SetHiddenThinkingLabel(label string) {
	m.thinkingLabelCalls = append(m.thinkingLabelCalls, label)
}
func (m *mockUIContext) SetWorkingIndicator(opts extension.WorkingIndicatorOptions) {
	m.workingIndCalls = append(m.workingIndCalls, opts)
}
func (m *mockUIContext) PasteToEditor(text string) {
	m.pasteToEditorCalls = append(m.pasteToEditorCalls, text)
}
func (m *mockUIContext) SetEditorText(text string) {
	m.setEditorTextCalls = append(m.setEditorTextCalls, text)
}
func (m *mockUIContext) GetEditorText() string { return m.editorText }
func (m *mockUIContext) SetFooter(factory any) {
	m.footerValue = factory
	if factory == nil {
		m.footerCleared = true
	}
}
func (m *mockUIContext) SetHeader(factory any) {
	m.headerCalls = append(m.headerCalls, factory)
	if lines, ok := factory.([]string); factory == nil || ok && len(lines) == 0 {
		m.headerCleared = true
	}
}
func (m *mockUIContext) SetLogin(definition extension.LoginDefinition) error {
	m.loginCalls = append(m.loginCalls, definition)
	return m.loginErr
}
func (m *mockUIContext) SetEditorComponent(factory any) {
	if factory == nil {
		m.editorCompCleared = true
	}
}
func (m *mockUIContext) GetEditorComponent() any        { return nil }
func (m *mockUIContext) GetToolsExpanded() bool         { return m.toolsExpanded }
func (m *mockUIContext) SetToolsExpanded(expanded bool) { m.toolsExpanded = expanded }
func (m *mockUIContext) RunRemoteOverlay(extension.RemoteOverlayOptions, extension.RemoteOverlayHost, func(extension.RemoteOverlayHandle)) (any, bool) {
	return nil, false
}
func (m *mockUIContext) OnRemoteTerminalInput(string, extension.RemoteTerminalInputHandler) func() {
	return func() {}
}
func (m *mockUIContext) Select(_ context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	return m.selectResult, m.selectErr
}
func (m *mockUIContext) Confirm(_ context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	return m.confirmResult, m.confirmErr
}
func (m *mockUIContext) Input(_ context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (string, error) {
	return m.inputResult, m.inputErr
}
func (m *mockUIContext) Editor(_ context.Context, _, _ string) (string, error) {
	return m.editorResult, m.editorErr
}
func (m *mockUIContext) SetTheme(theme any) extension.SetThemeResult { return m.themeResult }
func (m *mockUIContext) Theme() extension.Theme                      { return m.theme }
func (m *mockUIContext) GetAllThemes() []extension.ThemeMeta         { return m.allThemes }
func (m *mockUIContext) GetTheme(name string) (extension.Theme, error) {
	if m.namedTheme == nil {
		return nil, nil
	}
	return m.namedTheme, nil
}
func (m *mockUIContext) SetWidget(string, any, extension.ExtensionWidgetOptions)       {}
func (m *mockUIContext) OnTerminalInput(extension.TerminalInputHandler) func()         { return func() {} }
func (m *mockUIContext) Custom(context.Context, any, any) (any, error)                 { return nil, nil }
func (m *mockUIContext) AddAutocompleteProvider(extension.AutocompleteProviderFactory) {}

func newTestBridge(ui extension.UIContext) *UIBridge {
	b := NewUIBridge(func() {})
	b.SetUIContext(ui)
	return b
}

func call(b *UIBridge, method, argsJSON string) (*CallResultPayload, error) {
	return b.HandleCall("test-ext", &CallPayload{
		Method: method,
		Args:   json.RawMessage(argsJSON),
	})
}

func TestUIBridge_HandleCall_Notify(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)
	// notify uses notifyFunc if set
	var notified struct{ msg, level string }
	b.SetNotifyFunc(func(msg, level string) { notified.msg = msg; notified.level = level })

	res, err := call(b, "ui.notify", `{"message":"hello","level":"warn"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	if notified.msg != "hello" || notified.level != "warn" {
		t.Errorf("got notify(%q, %q), want (hello, warn)", notified.msg, notified.level)
	}
}

func TestUIBridge_HandleCall_Notify_DefaultLevel(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)
	// No notifyFunc set: falls back to ui.Notify

	_, err := call(b, "ui.notify", `{"message":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.notifyCalls) != 1 || mock.notifyCalls[0].kind != "info" {
		t.Errorf("expected default level 'info', got %+v", mock.notifyCalls)
	}
}

func TestUIBridge_HandleCall_SetStatus(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	res, err := call(b, "ui.setStatus", `{"key":"k","text":"v"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatal(res.Error.Message)
	}
	if len(mock.statusCalls) != 1 || mock.statusCalls[0].key != "k" || mock.statusCalls[0].text != "v" {
		t.Errorf("unexpected: %+v", mock.statusCalls)
	}
}

// TestUIBridge_HandleCall_SetStatus_BroadcastsStateChange mirrors upstream's
// setExtensionStatus (interactive-mode.ts:2203-2205), which updates the
// footer data provider and calls this.ui.requestRender() synchronously in
// the same in-process runtime. A subprocess footer factory that reads
// footerData.getExtensionStatuses() cannot observe that update on its own:
// it only re-renders when the host pushes a fresh state_update. Before the
// fix, ui.setStatus never notified the host to push one, so a footer
// composed from getExtensionStatuses() (as in
// parity/scenarios/extensions-runtime/testdata/ext/footer-status.mjs) could
// go stale forever after the initial render, most visibly across /reload
// (scenario 15-footer-status-reload-composition) where no other state
// transition happens to trigger an incidental re-render.
func TestUIBridge_HandleCall_SetStatus_BroadcastsStateChange(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	var broadcasts int
	b.OnStateChanged = func() { broadcasts++ }

	if _, err := call(b, "ui.setStatus", `{"key":"k","text":"v"}`); err != nil {
		t.Fatal(err)
	}
	if broadcasts != 1 {
		t.Fatalf("OnStateChanged called %d times, want 1", broadcasts)
	}
}

func TestUIBridge_HandleCall_GetFlag(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{
		GetFlag: func(extName, name string) any {
			if extName == "test-ext" && name == "plan" {
				return "auto"
			}
			return nil
		},
	})

	res, err := call(b, "getFlag", `{"name":"plan"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["value"] != "auto" {
		t.Fatalf("got %v", out)
	}
}

func TestUIBridge_HandleCall_SetTitle(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	_, err := call(b, "ui.setTitle", `{"title":"my title"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.titleCalls) != 1 || mock.titleCalls[0] != "my title" {
		t.Errorf("got %v", mock.titleCalls)
	}
}

func TestUIBridge_HandleCall_SetWorkingMessage(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	_, err := call(b, "ui.setWorkingMessage", `{"message":"loading..."}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.workingMsgCalls) != 1 || mock.workingMsgCalls[0] != "loading..." {
		t.Errorf("got %v", mock.workingMsgCalls)
	}
}

func TestUIBridge_HandleCall_Select(t *testing.T) {
	mock := &mockUIContext{selectResult: "b"}
	b := newTestBridge(mock)

	res, err := call(b, "ui.select", `{"title":"Pick","options":["a","b"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["selected"] != "b" || out["ok"] != true {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_Select_Error(t *testing.T) {
	mock := &mockUIContext{selectErr: errors.New("dialog failed")}
	b := newTestBridge(mock)

	res, _ := call(b, "ui.select", `{"title":"Pick","options":["a"]}`)
	if res.Error == nil || res.Error.Code != "ui_error" || res.Error.Message != "dialog failed" {
		t.Fatalf("expected specific UI error, got %+v", res)
	}
}

func TestUIBridge_HandleCall_Select_Cancelled(t *testing.T) {
	mock := &mockUIContext{selectErr: context.Canceled}
	b := newTestBridge(mock)

	res, _ := call(b, "ui.select", `{"title":"Pick","options":["a"]}`)
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["ok"] != false {
		t.Errorf("expected ok=false, got %v", out)
	}
}

func TestUIBridge_HandleCall_Confirm(t *testing.T) {
	mock := &mockUIContext{confirmResult: true}
	b := newTestBridge(mock)

	res, err := call(b, "ui.confirm", `{"title":"Sure?","message":"really?"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["confirmed"] != true {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_Input(t *testing.T) {
	mock := &mockUIContext{inputResult: "typed"}
	b := newTestBridge(mock)

	res, err := call(b, "ui.input", `{"title":"Name","placeholder":"enter"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["text"] != "typed" || out["ok"] != true {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_Editor(t *testing.T) {
	mock := &mockUIContext{editorResult: "edited text"}
	b := newTestBridge(mock)

	res, err := call(b, "ui.editor", `{"title":"Edit","prefill":"initial"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["text"] != "edited text" || out["ok"] != true {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_UnknownMethod(t *testing.T) {
	b := newTestBridge(&mockUIContext{})

	res, err := call(b, "nonexistent", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == nil || res.Error.Code != "unknown_method" {
		t.Errorf("expected unknown_method error, got %+v", res.Error)
	}
}

func TestUIBridge_HandleCall_GetEditorText(t *testing.T) {
	mock := &mockUIContext{editorText: "current text"}
	b := newTestBridge(mock)

	res, err := call(b, "ui.getEditorText", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["text"] != "current text" {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_SetEditorText(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	_, err := call(b, "ui.setEditorText", `{"text":"new text"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.setEditorTextCalls) != 1 || mock.setEditorTextCalls[0] != "new text" {
		t.Errorf("got %v", mock.setEditorTextCalls)
	}
}

func TestUIBridge_HandleCall_PasteToEditor(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	_, err := call(b, "ui.pasteToEditor", `{"text":"pasted"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mock.pasteToEditorCalls) != 1 || mock.pasteToEditorCalls[0] != "pasted" {
		t.Errorf("got %v", mock.pasteToEditorCalls)
	}
}

func TestUIBridge_HandleWidgetPush_ViaCall(t *testing.T) {
	invalidated := false
	b := NewUIBridge(func() { invalidated = true })

	b.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "w1", Lines: []string{"line1", "line2"}})

	proxy := b.GetWidget("ext1", "w1")
	if proxy == nil {
		t.Fatal("expected proxy, got nil")
	}
	lines := proxy.Lines()
	if len(lines) != 2 || lines[0] != "line1" {
		t.Errorf("got %v", lines)
	}
	if !invalidated {
		t.Error("expected invalidateTUI to be called")
	}
}

// A widget pushed before the interactive TUI wires its render callback (an
// extension that sets a widget while loading) must request renders through the
// live callback on later pushes. Otherwise a widget an extension updates from a
// timer stays frozen until something else repaints.
func TestUIBridge_WidgetPushedBeforeSetInvalidateUsesLiveCallback(t *testing.T) {
	b := NewUIBridge(func() {})
	b.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "clock", Lines: []string{"12:00"}})

	var renders atomic.Int32
	b.SetInvalidate(func() { renders.Add(1) })
	b.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "clock", Lines: []string{"12:01"}})
	if got := renders.Load(); got != 1 {
		t.Fatalf("update of an early widget requested %d renders, want 1", got)
	}
	b.GetWidget("ext1", "clock").Clear()
	if got := renders.Load(); got != 2 {
		t.Fatalf("clearing an early widget requested %d renders in total, want 2", got)
	}
}

func TestUIBridge_ClearExtension_AllKeys(t *testing.T) {
	b := NewUIBridge(func() {})
	b.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "a", Lines: []string{"x"}})
	b.HandleWidgetPush("ext1", &WidgetPushPayload{Key: "b", Lines: []string{"y"}})
	b.HandleWidgetPush("ext2", &WidgetPushPayload{Key: "c", Lines: []string{"z"}})

	b.ClearExtension("ext1")

	if b.GetWidget("ext1", "a") != nil || b.GetWidget("ext1", "b") != nil {
		t.Error("ext1 widgets should be cleared")
	}
	if b.GetWidget("ext2", "c") == nil {
		t.Error("ext2 widget should remain")
	}
}

func TestUIBridge_SetUIContext(t *testing.T) {
	b := NewUIBridge(func() {})
	// Initially noop: SetTitle does nothing
	_, _ = call(b, "ui.setTitle", `{"title":"x"}`)

	mock := &mockUIContext{}
	b.SetUIContext(mock)
	_, _ = call(b, "ui.setTitle", `{"title":"y"}`)

	if len(mock.titleCalls) != 1 || mock.titleCalls[0] != "y" {
		t.Errorf("expected title after SetUIContext, got %v", mock.titleCalls)
	}
}

func TestUIBridgeReplaysCurrentStateAcrossContextUpgrade(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&mockUIContext{})

	if _, err := bridge.handleSetStatus(json.RawMessage(`{"key":"build","text":"ready"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.handleSetFooter(json.RawMessage(`{"lines":["footer"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.handleSetHeader(json.RawMessage(`{"clear":true}`)); err != nil {
		t.Fatal(err)
	}
	current := &mockUIContext{}
	bridge.SetUIContext(current)

	if len(current.statusCalls) != 1 || current.statusCalls[0].text != "ready" {
		t.Fatalf("status calls = %#v", current.statusCalls)
	}
	lines, ok := current.footerValue.([]string)
	if !ok || len(lines) != 1 || lines[0] != "footer" {
		t.Fatalf("footer = %#v", current.footerValue)
	}
	if !current.headerCleared {
		t.Fatal("header clear was not replayed to replacement context")
	}
}

func TestUIBridge_HandleCall_SetTheme(t *testing.T) {
	mock := &mockUIContext{themeResult: extension.SetThemeResult{Success: true}}
	b := newTestBridge(mock)

	res, err := call(b, "ui.setTheme", `{"theme":"dark"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["success"] != true {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_GetToolsExpanded(t *testing.T) {
	mock := &mockUIContext{toolsExpanded: true}
	b := newTestBridge(mock)

	res, err := call(b, "ui.getToolsExpanded", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["expanded"] != true {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_SetToolsExpanded(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	_, err := call(b, "ui.setToolsExpanded", `{"expanded":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !mock.toolsExpanded {
		t.Error("expected toolsExpanded=true")
	}
}

func TestUIBridge_HandleCall_SetWidget_Clear(t *testing.T) {
	b := NewUIBridge(func() {})
	b.SetUIContext(&mockUIContext{})
	// First set a widget
	_, _ = call(b, "ui.setWidget", `{"key":"w","lines":["a","b"]}`)
	if b.GetWidget("test-ext", "w") == nil {
		t.Fatal("widget should exist")
	}
	// Now clear it (lines=null)
	_, _ = call(b, "ui.setWidget", `{"key":"w","lines":null}`)
	if b.GetWidget("test-ext", "w") != nil {
		t.Error("widget should be cleared")
	}
}

func TestUIBridge_HandleCall_SetWidget_RequestObserverPreservesPlacement(t *testing.T) {
	b := NewUIBridge(func() {})
	b.SetUIContext(&mockUIContext{})
	var key string
	var lines []string
	var placement string
	b.SetWidgetRequestFunc(func(_ string, gotKey string, gotLines []string, opts extension.ExtensionWidgetOptions) {
		key = gotKey
		lines = gotLines
		data, _ := json.Marshal(opts)
		var value struct {
			Placement string `json:"placement"`
		}
		_ = json.Unmarshal(data, &value)
		placement = value.Placement
	})
	if _, err := call(b, "ui.setWidget", `{"key":"w","content":["a","b"],"options":{"placement":"belowEditor"}}`); err != nil {
		t.Fatal(err)
	}
	if key != "w" || !slices.Equal(lines, []string{"a", "b"}) || placement != "belowEditor" {
		t.Fatalf("widget request = key:%q lines:%v placement:%q", key, lines, placement)
	}
}

func TestUIBridge_HandleCall_SendMessage_NotReady(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	// No actions set

	res, err := call(b, "sendMessage", `{"message":{"role":"assistant","content":"hi"},"options":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == nil || res.Error.Code != "not_ready" {
		t.Errorf("expected not_ready, got %+v", res.Error)
	}
}

func TestUIBridge_HandleCall_SendMessage(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	var gotMsg extension.CustomMessageRef
	var gotOpts SendMessageOptions
	b.SetActions(&HostCallbacks{
		SendMessage: func(msg extension.CustomMessageRef, opts SendMessageOptions) error {
			gotMsg = msg
			gotOpts = opts
			return nil
		},
	})

	res, err := call(b, "sendMessage", `{"message":{"customType":"notice","content":"hi","display":false,"details":{"a":1}},"options":{"triggerTurn":true,"deliverAs":"steer"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	if gotMsg.CustomType != "notice" || gotMsg.Content != "hi" || gotMsg.Display != false {
		t.Fatalf("message = %+v", gotMsg)
	}
	if gotOpts.TriggerTurn == nil || !*gotOpts.TriggerTurn || gotOpts.DeliverAs != "steer" {
		t.Fatalf("options = %+v", gotOpts)
	}
}

func TestUIBridge_HandleCall_SendUserMessage(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	var gotContent any
	var gotOpts SendUserMessageOptions
	b.SetActions(&HostCallbacks{
		SendUserMessage: func(content any, opts SendUserMessageOptions) error {
			gotContent = content
			gotOpts = opts
			return nil
		},
	})

	res, err := call(b, "sendUserMessage", `{"content":"hello","options":{"deliverAs":"followUp"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	if gotContent != "hello" || gotOpts.DeliverAs != "followUp" {
		t.Fatalf("got content/options = %q/%+v", gotContent, gotOpts)
	}
}

func TestUIBridge_HandleCall_AppendEntry(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	var gotType string
	var gotData any
	b.SetActions(&HostCallbacks{
		AppendEntry: func(customType string, data any) error {
			gotType = customType
			gotData = data
			return nil
		},
	})

	res, err := call(b, "appendEntry", `{"customType":"note","data":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected error: %s", res.Error.Message)
	}
	if gotType != "note" || gotData != "hello" {
		t.Fatalf("got type/data = %q/%v", gotType, gotData)
	}
}

func TestUIBridge_HandleCall_SetSessionNamePropagatesError(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{
		SetSessionName: func(string) error { return errors.New("cannot set name") },
	})

	res, err := call(b, "setSessionName", `{"name":"work"}`)
	if err == nil || !strings.Contains(err.Error(), "cannot set name") {
		t.Fatalf("err = %v, result = %+v; want propagated setSessionName error", err, res)
	}
}

func TestUIBridge_HandleCall_GetSessionName(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{
		GetSessionName: func() string { return "my-session" },
	})

	res, err := call(b, "getSessionName", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["name"] != "my-session" {
		t.Errorf("got %v", out)
	}
}

func TestUIBridge_HandleCall_SetModel(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	var modelSet string
	b.SetActions(&HostCallbacks{
		SetModel: func(_ context.Context, m string) (bool, error) { modelSet = m; return true, nil },
	})

	res, err := call(b, "setModel", `{"model":"gpt-4"}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["success"] != true || modelSet != "gpt-4" {
		t.Errorf("got %v, modelSet=%q", out, modelSet)
	}
}

func TestUIBridge_HandleCall_Custom_Unsupported(t *testing.T) {
	b := newTestBridge(&mockUIContext{})

	// SDK Go/Rust extensions pass no key and are still treated as
	// upstream-incompatible (factory closures can't cross a socket).
	res, err := call(b, "ui.custom", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == nil || res.Error.Code != "unsupported" {
		t.Errorf("expected unsupported, got %+v", res.Error)
	}
}

func TestFocusedInputDeliveryFailureClosesOverlay(t *testing.T) {
	bridge := NewUIBridge(func() {})
	proxy := bridge.customOverlayProxy("ext", "focused")
	target := &recordingRemoteOverlayHandle{closed: make(chan struct{})}
	proxy.SetTarget(target)

	bridge.sendCustomInput("ext", nil, "focused", "x")
	select {
	case <-target.closed:
	case <-time.After(time.Second):
		t.Fatal("focused overlay remained open after input delivery failed")
	}
	if _, ok := target.result.(remoteOverlayError); !ok {
		t.Fatalf("close result = %#v, want remoteOverlayError", target.result)
	}
}

func TestRegisterExtConnPublishesBootstrapOnlyToNewConnection(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getModels", func() []map[string]any {
		return []map[string]any{{"provider": "test", "id": "model"}}
	})
	oldHost, oldPeer := net.Pipe()
	defer func() { _ = oldHost.Close() }()
	defer func() { _ = oldPeer.Close() }()
	oldConn := NewConn("old", oldHost)
	bridge.RegisterExtConn("old", oldConn)
	select {
	case <-oldConn.outCh:
	case <-time.After(time.Second):
		t.Fatal("old connection did not receive its bootstrap catalog")
	}

	newHost, newPeer := net.Pipe()
	defer func() { _ = newHost.Close() }()
	defer func() { _ = newPeer.Close() }()
	newConn := NewConn("new", newHost)
	bridge.RegisterExtConn("new", newConn)
	select {
	case <-newConn.outCh:
	case <-time.After(time.Second):
		t.Fatal("new connection did not receive its bootstrap catalog")
	}
	select {
	case <-oldConn.outCh:
		t.Fatal("registering a new connection republished bootstrap state to an existing connection")
	default:
	}
}

// Building a catalog resolves every model's auth, so publishing to nobody is
// pure startup cost: interactive startup publishes when it wires model
// operations and again after refreshing catalogs, often with no extension
// connected.
func TestPublishModelCatalogBuildsNothingWithoutConnections(t *testing.T) {
	bridge := NewUIBridge(func() {})
	builds := 0
	bridge.SetHostAction("getModels", func() []map[string]any {
		builds++
		return []map[string]any{{"provider": "test", "id": "model"}}
	})
	bridge.PublishModelCatalog()
	bridge.PublishModelCatalog()
	if builds != 0 {
		t.Fatalf("catalog builds without connections = %d, want 0", builds)
	}

	host, peer := net.Pipe()
	defer func() { _ = host.Close() }()
	defer func() { _ = peer.Close() }()
	conn := NewConn("ext", host)
	bridge.RegisterExtConn("ext", conn)
	<-conn.outCh
	bridge.PublishModelCatalog()
	select {
	case <-conn.outCh:
	case <-time.After(time.Second):
		t.Fatal("connected extension did not receive the published catalog")
	}
	if builds != 2 {
		t.Fatalf("catalog builds = %d, want bootstrap plus one publication", builds)
	}
}

func TestFocusedReloadCleanupCannotCloseReplacementGeneration(t *testing.T) {
	bridge := NewUIBridge(func() {})
	oldHost, oldPeer := net.Pipe()
	defer func() { _ = oldHost.Close() }()
	defer func() { _ = oldPeer.Close() }()
	newHost, newPeer := net.Pipe()
	defer func() { _ = newHost.Close() }()
	defer func() { _ = newPeer.Close() }()
	oldConn := NewConn("old", oldHost)
	newConn := NewConn("new", newHost)
	bridge.RegisterExtConn("ext", oldConn)
	oldTarget := &recordingRemoteOverlayHandle{closed: make(chan struct{})}
	bridge.customOverlayProxyFor("ext", oldConn, "focused").SetTarget(oldTarget)
	bridge.RegisterExtConn("ext", newConn)
	newTarget := &recordingRemoteOverlayHandle{closed: make(chan struct{})}
	bridge.customOverlayProxyFor("ext", newConn, "focused").SetTarget(newTarget)

	bridge.ClearExtensionConn("ext", oldConn)
	select {
	case <-oldTarget.closed:
	default:
		t.Fatal("old focused generation remained open")
	}
	select {
	case <-newTarget.closed:
		t.Fatal("old cleanup closed the replacement focused generation")
	default:
	}
	if bridge.extConns["ext"] != newConn {
		t.Fatal("old cleanup removed the replacement connection")
	}
}

type serialFocusedUI struct {
	extension.UIContext
	entered chan string
	release chan struct{}
}

func (u *serialFocusedUI) Select(ctx context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.entered <- title
	select {
	case <-u.release:
		return "selected", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (u *serialFocusedUI) RunRemoteOverlay(opts extension.RemoteOverlayOptions, _ extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	onHandle(&capturingRemoteOverlayHandle{})
	u.entered <- opts.Title
	<-u.release
	return opts.Title, true
}

func TestFocusedOverlaysSerializeTerminalFocus(t *testing.T) {
	ui := &serialFocusedUI{
		UIContext: extension.NoopUIContext,
		entered:   make(chan string, 2),
		release:   make(chan struct{}, 2),
	}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	callOverlay := func(key, title string) <-chan error {
		done := make(chan error, 1)
		args, err := json.Marshal(RemoteOverlayOpenPayload{Key: key, Title: title})
		if err != nil {
			done <- err
			return done
		}
		go func() {
			result, err := bridge.handleCall(context.Background(), "ext", nil, &CallPayload{Method: CallUICustom, Args: args})
			if err == nil && result.Error != nil {
				err = result.Error.ToError()
			}
			done <- err
		}()
		return done
	}

	firstDone := callOverlay("custom-1", "first")
	select {
	case got := <-ui.entered:
		if got != "first" {
			t.Fatalf("first focused overlay = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first focused overlay did not acquire focus")
	}
	secondDone := callOverlay("custom-2", "second")
	select {
	case got := <-ui.entered:
		t.Fatalf("second focused overlay entered while first still owned focus: %q", got)
	case <-time.After(50 * time.Millisecond):
	}

	ui.release <- struct{}{}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ui.entered:
		if got != "second" {
			t.Fatalf("second focused overlay = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second focused overlay did not acquire released focus")
	}
	ui.release <- struct{}{}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveDialogsWaitForFocusedOverlay(t *testing.T) {
	ui := &serialFocusedUI{
		UIContext: extension.NoopUIContext,
		entered:   make(chan string, 2),
		release:   make(chan struct{}, 2),
	}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	customArgs, _ := json.Marshal(RemoteOverlayOpenPayload{Key: "custom-1", Title: "runner"})
	customDone := make(chan struct{})
	go func() {
		_, _ = bridge.handleCall(context.Background(), "ext", nil, &CallPayload{Method: CallUICustom, Args: customArgs})
		close(customDone)
	}()
	select {
	case <-ui.entered:
	case <-time.After(time.Second):
		t.Fatal("focused overlay did not acquire focus")
	}

	selectArgs := json.RawMessage(`{"title":"selector","options":["selected"]}`)
	selectDone := make(chan struct{})
	go func() {
		_, _ = bridge.handleCall(context.Background(), "other", nil, &CallPayload{Method: "ui.select", Args: selectArgs})
		close(selectDone)
	}()
	select {
	case got := <-ui.entered:
		t.Fatalf("selector entered while custom overlay owned focus: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
	ui.release <- struct{}{}
	<-customDone
	select {
	case got := <-ui.entered:
		if got != "selector" {
			t.Fatalf("queued selector title = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("selector did not acquire released focus")
	}
	ui.release <- struct{}{}
	<-selectDone
}

func TestFocusedOverlayWaitCancelsWithoutTakingFocus(t *testing.T) {
	ui := &serialFocusedUI{
		UIContext: extension.NoopUIContext,
		entered:   make(chan string, 2),
		release:   make(chan struct{}, 1),
	}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	firstArgs, _ := json.Marshal(RemoteOverlayOpenPayload{Key: "custom-1", Title: "first"})
	firstDone := make(chan struct{})
	go func() {
		_, _ = bridge.handleCall(context.Background(), "ext", nil, &CallPayload{Method: CallUICustom, Args: firstArgs})
		close(firstDone)
	}()
	select {
	case <-ui.entered:
	case <-time.After(time.Second):
		t.Fatal("first focused overlay did not acquire focus")
	}

	ctx, cancel := context.WithCancel(context.Background())
	secondArgs, _ := json.Marshal(RemoteOverlayOpenPayload{Key: "custom-2", Title: "second"})
	secondDone := make(chan *CallResultPayload, 1)
	go func() {
		result, _ := bridge.handleCall(ctx, "ext", nil, &CallPayload{Method: CallUICustom, Args: secondArgs})
		secondDone <- result
	}()
	cancel()
	select {
	case result := <-secondDone:
		if result == nil || result.Error == nil || result.Error.Code != "cancelled" {
			t.Fatalf("cancelled focus waiter result = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled focus waiter did not return")
	}

	ui.release <- struct{}{}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first focused overlay did not close")
	}
	select {
	case got := <-ui.entered:
		t.Fatalf("cancelled overlay later acquired focus: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManyFocusedWaitersCancelWithoutLeakingFocus(t *testing.T) {
	ui := &serialFocusedUI{
		UIContext: extension.NoopUIContext,
		entered:   make(chan string, 2),
		release:   make(chan struct{}, 2),
	}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	firstArgs, _ := json.Marshal(RemoteOverlayOpenPayload{Key: "custom-owner", Title: "owner"})
	firstDone := make(chan struct{})
	go func() {
		_, _ = bridge.handleCall(context.Background(), "owner", nil, &CallPayload{Method: CallUICustom, Args: firstArgs})
		close(firstDone)
	}()
	select {
	case <-ui.entered:
	case <-time.After(time.Second):
		t.Fatal("focus owner did not enter")
	}

	const waiters = 128
	results := make(chan *CallResultPayload, waiters)
	cancels := make([]context.CancelFunc, 0, waiters)
	for index := range waiters {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		args, _ := json.Marshal(RemoteOverlayOpenPayload{Key: fmt.Sprintf("custom-%d", index), Title: fmt.Sprintf("waiter-%d", index)})
		go func() {
			result, _ := bridge.handleCall(ctx, "waiter", nil, &CallPayload{Method: CallUICustom, Args: args})
			results <- result
		}()
	}
	for _, cancel := range cancels {
		cancel()
	}
	for range waiters {
		select {
		case result := <-results:
			if result == nil || result.Error == nil || result.Error.Code != "cancelled" {
				t.Fatalf("focus waiter result = %+v", result)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("focus waiter did not cancel")
		}
	}
	select {
	case entered := <-ui.entered:
		t.Fatalf("cancelled waiter acquired focus: %q", entered)
	default:
	}

	ui.release <- struct{}{}
	<-firstDone
	finalArgs, _ := json.Marshal(RemoteOverlayOpenPayload{Key: "custom-final", Title: "final"})
	finalDone := make(chan struct{})
	go func() {
		_, _ = bridge.handleCall(context.Background(), "final", nil, &CallPayload{Method: CallUICustom, Args: finalArgs})
		close(finalDone)
	}()
	select {
	case entered := <-ui.entered:
		if entered != "final" {
			t.Fatalf("focus after waiter cancellation = %q", entered)
		}
	case <-time.After(time.Second):
		t.Fatal("focus token leaked after waiter cancellation")
	}
	ui.release <- struct{}{}
	<-finalDone
}

func TestFocusedLateFrameDoesNotRecreateCompletedOverlay(t *testing.T) {
	bridge := NewUIBridge(func() {})
	hostConn, peerConn := net.Pipe()
	defer func() {
		if err := hostConn.Close(); err != nil {
			t.Errorf("close host connection: %v", err)
		}
	}()
	defer func() {
		if err := peerConn.Close(); err != nil {
			t.Errorf("close peer connection: %v", err)
		}
	}()
	owner := NewConn("ext", hostConn)
	args, err := json.Marshal(RemoteOverlayOpenPayload{Key: "custom-1"})
	if err != nil {
		t.Fatal(err)
	}
	bridge.reserveCustomOverlay("ext", owner, args)
	bridge.unregisterCustomOverlay("ext", owner, "custom-1")

	renderArgs, err := json.Marshal(RemoteOverlayRenderPayload{Key: "custom-1", Lines: []string{"late"}, Width: 80, Seq: 2})
	if err != nil {
		t.Fatal(err)
	}
	bridge.HandleNotifyFrom("ext", owner, &NotifyPayload{Method: NotifyUICustomRender, Args: renderArgs})

	bridge.mu.RLock()
	remaining := len(bridge.customOverlays)
	bridge.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("late frame recreated %d completed overlay entries", remaining)
	}
}

func TestFocusedProxyRejectsSecondTarget(t *testing.T) {
	proxy := newOverlayProxy()
	first := &recordingRemoteOverlayHandle{closed: make(chan struct{})}
	second := &recordingRemoteOverlayHandle{closed: make(chan struct{})}
	proxy.SetTarget(first)
	proxy.SetTarget(second)
	select {
	case <-second.closed:
		if _, ok := second.result.(remoteOverlayError); !ok {
			t.Fatalf("second target result = %#v", second.result)
		}
	case <-time.After(time.Second):
		t.Fatal("second target was not rejected")
	}
	select {
	case <-first.closed:
		t.Fatal("rejecting second target closed the active target")
	default:
	}
}

func TestFocusedCloseBarrierIsIdempotent(t *testing.T) {
	proxy := newOverlayProxy()
	target := &recordingRemoteOverlayHandle{closed: make(chan struct{})}
	proxy.SetTarget(target)
	proxy.Close("first")
	proxy.Close("second")
	if target.result != "first" {
		t.Fatalf("duplicate close replaced result with %#v", target.result)
	}
}

func TestFocusedFrameRejectsStaleWidthAndSequence(t *testing.T) {
	proxy := newOverlayProxy()
	target := &capturingRemoteOverlayHandle{}
	proxy.SetTarget(target)
	proxy.UpdateFrame([]string{"current"}, 100, 2, 100)
	proxy.UpdateFrame([]string{"old-sequence"}, 100, 1, 100)
	proxy.UpdateFrame([]string{"old-width"}, 80, 3, 100)
	if len(target.lines) != 1 || target.lines[0] != "current" {
		t.Fatalf("accepted stale focused frame: %v", target.lines)
	}
}

type capturingRemoteOverlayHandle struct {
	lines []string
}

func (h *capturingRemoteOverlayHandle) UpdateLines(lines []string) {
	h.lines = append([]string(nil), lines...)
}
func (*capturingRemoteOverlayHandle) Close(any) {}

type recordingRemoteOverlayHandle struct {
	result any
	closed chan struct{}
}

func (*recordingRemoteOverlayHandle) UpdateLines([]string) {}
func (h *recordingRemoteOverlayHandle) Close(result any) {
	h.result = result
	close(h.closed)
}

func TestUIBridgeClearExtensionRemovesWidgetsFromMountedSet(t *testing.T) {
	bridge := NewUIBridge(func() {})
	counts := make(chan int, 2)
	bridge.SetWidgetSyncFunc(func(widgets map[string]*PushProxy) { counts <- len(widgets) })
	bridge.HandleWidgetPush("ext", &WidgetPushPayload{Key: "status", Lines: []string{"ready"}})
	bridge.ClearExtension("ext")
	for _, want := range []int{1, 0} {
		select {
		case got := <-counts:
			if got != want {
				t.Fatalf("mounted widget count = %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("mounted widget count %d was not published", want)
		}
	}
}

func TestUIBridge_HandleCall_SetFooter_Clear(t *testing.T) {
	mock := &mockUIContext{}
	b := newTestBridge(mock)

	_, err := call(b, "ui.setFooter", `{"clear":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !mock.footerCleared {
		t.Error("expected footer to be cleared")
	}
}

func TestUIBridgeReplaysPreTUIStatusAndFooter(t *testing.T) {
	bridge := NewUIBridge(func() {})
	if _, err := call(bridge, "ui.setStatus", `{"key":"startup","text":"ready"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(bridge, "ui.setFooter", `{"lines":["startup-footer"]}`); err != nil {
		t.Fatal(err)
	}
	ui := &mockUIContext{}
	bridge.SetUIContext(ui)
	if len(ui.statusCalls) != 1 || ui.statusCalls[0].key != "startup" || ui.statusCalls[0].text != "ready" {
		t.Fatalf("status replay = %+v", ui.statusCalls)
	}
	lines, ok := ui.footerValue.([]string)
	if !ok || len(lines) != 1 || lines[0] != "startup-footer" {
		t.Fatalf("footer replay = %#v", ui.footerValue)
	}
}

func TestUIBridge_AllWidgets(t *testing.T) {
	b := NewUIBridge(func() {})
	b.HandleWidgetPush("e1", &WidgetPushPayload{Key: "a", Lines: []string{"1"}})
	b.HandleWidgetPush("e2", &WidgetPushPayload{Key: "b", Lines: []string{"2"}})

	all := b.AllWidgets()
	if len(all) != 2 {
		t.Errorf("expected 2 widgets, got %d", len(all))
	}
}

func TestUIBridge_SetUIContext_Nil(t *testing.T) {
	b := NewUIBridge(func() {})
	b.SetUIContext(nil)
	// Should fall back to NoopUIContext, not panic
	res, err := call(b, "ui.getEditorText", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Agent control bridge unit tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestUIBridge_IsIdle(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})

	res, err := call(b, "isIdle", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["idle"] != true {
		t.Errorf("idle = %v, want true", out["idle"])
	}
}

func TestUIBridge_Abort(t *testing.T) {
	aborted := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Abort: func() { aborted = true }})

	res, err := call(b, "abort", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
	if !aborted {
		t.Error("abort callback not called")
	}
}

func TestUIBridge_HasPendingMessages(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{HasPendingMessages: func() bool { return true }})

	res, err := call(b, "hasPendingMessages", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.Result, &out)
	if out["pending"] != true {
		t.Errorf("pending = %v, want true", out["pending"])
	}
}

func TestUIBridge_Shutdown(t *testing.T) {
	shutdown := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Shutdown: func() { shutdown = true }})

	_, err := call(b, "shutdown", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !shutdown {
		t.Error("shutdown callback not called")
	}
}

func TestUIBridge_Compact(t *testing.T) {
	compacted := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Compact: func(context.Context, *extension.CompactOptions) { compacted = true }})

	_, err := call(b, "compact", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !compacted {
		t.Error("compact callback not called")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Session control bridge unit tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestUIBridge_WaitForIdle(t *testing.T) {
	waited := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{WaitForIdle: func(context.Context) error { waited = true; return nil }})

	res, err := call(b, "waitForIdle", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
	if !waited {
		t.Error("waitForIdle callback not called")
	}
}

func TestUIBridge_NewSession_Unsupported(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	// No NewSession callback set → unsupported

	res, err := call(b, "newSession", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == nil || res.Error.Code != "unsupported" {
		t.Errorf("expected unsupported, got %+v", res.Error)
	}
}

func TestUIBridge_Fork(t *testing.T) {
	forked := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Fork: func(_ context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
		forked = true
		return extension.CancelledResult{Cancelled: false}, nil
	}})

	res, err := call(b, "fork", `{"entryId":"e1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
	if !forked {
		t.Error("fork callback not called")
	}
}

func TestUIBridge_NavigateTree(t *testing.T) {
	navigated := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{NavigateTree: func(_ context.Context, targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
		navigated = true
		if targetID != "t1" {
			t.Errorf("targetID = %q, want t1", targetID)
		}
		return extension.CancelledResult{Cancelled: false}, nil
	}})

	res, err := call(b, "navigateTree", `{"targetId":"t1","summarize":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
	if !navigated {
		t.Error("navigateTree callback not called")
	}
}

func TestUIBridge_SwitchSession(t *testing.T) {
	switched := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{SwitchSession: func(_ context.Context, path string, opts *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
		switched = true
		return extension.CancelledResult{Cancelled: false}, nil
	}})

	res, err := call(b, "switchSession", `{"sessionPath":"/tmp/s.jsonl"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
	if !switched {
		t.Error("switchSession callback not called")
	}
}

func TestUIBridge_Reload(t *testing.T) {
	reloaded := false
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Reload: func(context.Context) error { reloaded = true; return nil }})

	res, err := call(b, "reload", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != nil {
		t.Errorf("unexpected error: %s", res.Error.Message)
	}
	if !reloaded {
		t.Error("reload callback not called")
	}
}

// A theme reaches an extension as the structured object the host holds. The
// ui.theme and ui.getTheme handlers used to render it with fmt.Sprintf("%v"),
// which turns a theme struct into a Go debug string. Every SDK unmarshals the
// result into an untyped value, so nothing failed: Go, Rust, and Python all
// accepted "&{dark map[...]}" as a theme and could not read a single field
// from it. The theme_change notify path always marshalled correctly, so the
// same theme was structured when pushed and stringified when queried.
func TestThemeQueriesReturnStructuredThemeNotDebugString(t *testing.T) {
	type paletteTheme struct {
		Name       string `json:"name"`
		Background string `json:"background"`
	}
	current := paletteTheme{Name: "current", Background: "#101010"}
	named := paletteTheme{Name: "solarized", Background: "#002b36"}
	b := newTestBridge(&mockUIContext{theme: current, namedTheme: named})

	for _, tc := range []struct {
		method string
		args   string
		want   paletteTheme
	}{
		{method: "ui.theme", want: current},
		{method: "ui.getTheme", args: `{"name":"solarized"}`, want: named},
	} {
		t.Run(tc.method, func(t *testing.T) {
			res, err := b.HandleCall("ext", &CallPayload{Method: tc.method, Args: json.RawMessage(tc.args)})
			if err != nil {
				t.Fatalf("%s: %v", tc.method, err)
			}
			if res.Error != nil {
				t.Fatalf("%s: %+v", tc.method, res.Error)
			}
			var got struct {
				Theme paletteTheme `json:"theme"`
			}
			if err := json.Unmarshal(res.Result, &got); err != nil {
				t.Fatalf("%s returned a value no SDK can decode as a theme: %v (raw %s)", tc.method, err, res.Result)
			}
			if got.Theme != tc.want {
				t.Errorf("%s theme = %+v, want %+v (raw %s)", tc.method, got.Theme, tc.want, res.Result)
			}
		})
	}
}

func validLoginJSON(t *testing.T, name string) []byte {
	t.Helper()
	definition := extension.LoginDefinition{
		Brand:       []string{strings.Repeat("A", 41), strings.Repeat("A", 41), strings.Repeat("A", 41), strings.Repeat("A", 41), strings.Repeat("A", 41)},
		Hero:        slices.Repeat([]string{strings.Repeat("A", 32)}, 14),
		Mascot:      []string{strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16), strings.Repeat("A", 16)},
		Palette:     map[string]string{"A": "#112233"},
		Name:        name,
		Description: "description",
		Tagline:     "tagline",
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestUIBridgeSetLoginImmediate(t *testing.T) {
	ui := &mockUIContext{}
	bridge := newTestBridge(ui)
	result, err := bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "immediate")})
	if err != nil || result.Error != nil {
		t.Fatalf("set login = (%+v, %v)", result, err)
	}
	if len(ui.loginCalls) != 1 || ui.loginCalls[0].Name != "immediate" {
		t.Fatalf("login calls = %#v", ui.loginCalls)
	}
}

func TestUIBridgeSetLoginPendingAppliesOnce(t *testing.T) {
	bridge := NewUIBridge(func() {})
	if result, err := bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "pending")}); err != nil || result.Error != nil {
		t.Fatalf("set login = (%+v, %v)", result, err)
	}
	first := &mockUIContext{}
	bridge.SetUIContext(first)
	second := &mockUIContext{}
	bridge.SetUIContext(second)
	if len(first.loginCalls) != 1 || first.loginCalls[0].Name != "pending" {
		t.Fatalf("first login calls = %#v", first.loginCalls)
	}
	if len(second.loginCalls) != 0 {
		t.Fatalf("pending login applied more than once: %#v", second.loginCalls)
	}
}

func TestUIBridgeLoginAndHeaderSharePendingSlot(t *testing.T) {
	t.Run("login then header", func(t *testing.T) {
		bridge := NewUIBridge(func() {})
		_, _ = bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "login")})
		_, _ = bridge.handleSetHeader(json.RawMessage(`{"lines":["header"]}`))
		ui := &mockUIContext{}
		bridge.SetUIContext(ui)
		if len(ui.loginCalls) != 0 || len(ui.headerCalls) != 1 {
			t.Fatalf("login calls = %d, header calls = %#v", len(ui.loginCalls), ui.headerCalls)
		}
	})
	t.Run("header then login", func(t *testing.T) {
		bridge := NewUIBridge(func() {})
		_, _ = bridge.handleSetHeader(json.RawMessage(`{"lines":["header"]}`))
		_, _ = bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "login")})
		ui := &mockUIContext{}
		bridge.SetUIContext(ui)
		if len(ui.headerCalls) != 0 || len(ui.loginCalls) != 1 || ui.loginCalls[0].Name != "login" {
			t.Fatalf("header calls = %#v, login calls = %#v", ui.headerCalls, ui.loginCalls)
		}
	})
	t.Run("clear wins and restores default", func(t *testing.T) {
		bridge := NewUIBridge(func() {})
		_, _ = bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "login")})
		_, _ = bridge.handleSetHeader(json.RawMessage(`{"clear":true}`))
		ui := &mockUIContext{}
		bridge.SetUIContext(ui)
		if len(ui.loginCalls) != 0 || len(ui.headerCalls) != 1 || !ui.headerCleared {
			t.Fatalf("header clear not applied: headers=%#v logins=%#v", ui.headerCalls, ui.loginCalls)
		}
	})
}

func TestUIBridgeSetLoginRejectsInvalidWithoutReplacingState(t *testing.T) {
	cases := map[string][]byte{
		"malformed":          []byte(`{"brand":`),
		"duplicate palette":  []byte(strings.Replace(string(validLoginJSON(t, "valid")), `"palette":{"A":"#112233"}`, `"palette":{"A":"#112233","A":"#445566"}`, 1)),
		"unknown field":      []byte(strings.Replace(string(validLoginJSON(t, "valid")), `"name":`, `"unknown":true,"name":`, 1)),
		"invalid definition": []byte(strings.Replace(string(validLoginJSON(t, "valid")), strings.Repeat("A", 41), strings.Repeat("A", 40), 1)),
	}
	for name, invalid := range cases {
		t.Run(name+" pending", func(t *testing.T) {
			bridge := NewUIBridge(func() {})
			_, _ = bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "preserved")})
			result, err := bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: invalid})
			if err != nil || result.Error == nil || result.Error.Code != "invalid_login" || result.Error.Message == "" {
				t.Fatalf("invalid result = (%+v, %v)", result, err)
			}
			ui := &mockUIContext{}
			bridge.SetUIContext(ui)
			if len(ui.loginCalls) != 1 || ui.loginCalls[0].Name != "preserved" {
				t.Fatalf("prior pending login not preserved: %#v", ui.loginCalls)
			}
		})
	}

	ui := &mockUIContext{}
	bridge := newTestBridge(ui)
	_, _ = bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "active")})
	result, _ := bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: cases["invalid definition"]})
	if result.Error == nil || len(ui.loginCalls) != 1 || ui.loginCalls[0].Name != "active" {
		t.Fatalf("invalid login replaced active state: result=%+v calls=%#v", result, ui.loginCalls)
	}
}

func TestUIBridgeSetLoginDefensivelyCopiesPendingData(t *testing.T) {
	bridge := NewUIBridge(func() {})
	raw := validLoginJSON(t, "original")
	call := &CallPayload{Method: CallUISetLogin, Args: raw}
	result, err := bridge.HandleCall("test-ext", call)
	if err != nil || result.Error != nil {
		t.Fatalf("set login = (%+v, %v)", result, err)
	}
	copy(raw, []byte(strings.Repeat(" ", len(raw))))
	ui := &mockUIContext{}
	bridge.SetUIContext(ui)
	if len(ui.loginCalls) != 1 || ui.loginCalls[0].Name != "original" || ui.loginCalls[0].Palette["A"] != "#112233" {
		t.Fatalf("pending login changed with call data: %#v", ui.loginCalls)
	}
}

func TestUIBridgePendingLoginSurvivesUIApplyError(t *testing.T) {
	bridge := NewUIBridge(func() {})
	result, err := bridge.HandleCall("test-ext", &CallPayload{Method: CallUISetLogin, Args: validLoginJSON(t, "pending")})
	if err != nil || result.Error != nil {
		t.Fatalf("set login = (%+v, %v)", result, err)
	}

	failing := &mockUIContext{loginErr: errors.New("render unavailable")}
	bridge.SetUIContext(failing)
	if len(failing.loginCalls) != 1 {
		t.Fatalf("failing UI login calls = %#v", failing.loginCalls)
	}

	working := &mockUIContext{}
	bridge.SetUIContext(working)
	if len(working.loginCalls) != 1 || working.loginCalls[0].Name != "pending" {
		t.Fatalf("pending login was lost after UI error: %#v", working.loginCalls)
	}
}

// BindCommandActions exposes the session actions to subprocess extensions;
// the flat wire arguments reach the bound action.
func TestUIBridge_BindCommandActionsNavigateTree(t *testing.T) {
	b := NewUIBridge(func() {})
	var gotTarget string
	var gotOptions *extension.NavigateTreeOptions
	b.BindCommandActions(extension.CommandActions{NavigateTree: func(target string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
		gotTarget, gotOptions = target, opts
		return extension.CancelledResult{}, nil
	}})
	res, err := call(b, "navigateTree", `{"targetId":"entry-1","summarize":true,"customInstructions":"focus"}`)
	if err != nil || res.Error != nil {
		t.Fatalf("navigateTree = %+v, %v", res, err)
	}
	if gotTarget != "entry-1" || gotOptions == nil || !gotOptions.Summarize || gotOptions.CustomInstructions != "focus" {
		t.Fatalf("bound action got %q %+v", gotTarget, gotOptions)
	}
}
