package cli

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type rpcUIResponse struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Value     string `json:"value,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

// RPCUIMethod is the "method" member of an RpcExtensionUIRequest: the closed union in rpc-types.ts RpcExtensionUIRequest.
type RPCUIMethod string

// The request methods of rpc-types.ts RpcExtensionUIRequest.
const (
	RPCUIMethodSelect        RPCUIMethod = "select"
	RPCUIMethodConfirm       RPCUIMethod = "confirm"
	RPCUIMethodInput         RPCUIMethod = "input"
	RPCUIMethodEditor        RPCUIMethod = "editor"
	RPCUIMethodNotify        RPCUIMethod = "notify"
	RPCUIMethodSetStatus     RPCUIMethod = "setStatus"
	RPCUIMethodSetWidget     RPCUIMethod = "setWidget"
	RPCUIMethodSetTitle      RPCUIMethod = "setTitle"
	RPCUIMethodSetEditorText RPCUIMethod = "set_editor_text"
)

type rpcUIRequest struct {
	Type            string      `json:"type"`
	ID              string      `json:"id"`
	Method          RPCUIMethod `json:"method"`
	Title           string      `json:"title,omitempty"`
	Options         []string    `json:"options,omitempty"`
	Message         string      `json:"message,omitempty"`
	Placeholder     *string     `json:"placeholder,omitempty"`
	Prefill         *string     `json:"prefill,omitempty"`
	Timeout         *float64    `json:"timeout,omitempty"`
	NotifyType      string      `json:"notifyType,omitempty"`
	StatusKey       string      `json:"statusKey,omitempty"`
	StatusText      *string     `json:"statusText,omitempty"`
	WidgetKey       string      `json:"widgetKey,omitempty"`
	WidgetLines     *[]string   `json:"widgetLines,omitempty"`
	WidgetPlacement string      `json:"widgetPlacement,omitempty"`
	Text            string      `json:"text,omitempty"`
}

type rpcUIContext struct {
	output func(any)

	mu        sync.Mutex
	pending   map[string]chan rpcUIResponse
	published map[string]struct{}
	closed    bool
	// pendingWaiters are closed when a dialog starts waiting for a response.
	pendingWaiters []chan struct{}
}

func newRPCUIContext(output func(any)) *rpcUIContext {
	return &rpcUIContext{output: output, pending: make(map[string]chan rpcUIResponse), published: make(map[string]struct{})}
}

func (u *rpcUIContext) request(ctx context.Context, request rpcUIRequest, opts extension.ExtensionUIDialogOptions) (rpcUIResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := uuid.NewString()
	request.Type = "extension_ui_request"
	request.ID = id
	timeoutMs, hasTimeout := rpcDialogTimeout(opts)
	if hasTimeout {
		request.Timeout = &timeoutMs
	}

	responses := make(chan rpcUIResponse, 1)
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return rpcUIResponse{}, context.Canceled
	}
	u.pending[id] = responses
	u.mu.Unlock()

	u.output(request)

	u.mu.Lock()
	if u.pending[id] == responses {
		u.published[id] = struct{}{}
		for _, waiter := range u.pendingWaiters {
			close(waiter)
		}
		u.pendingWaiters = nil
	}
	u.mu.Unlock()
	// Pi writes the request synchronously when the dialog opens; the dialog is installed once the request is written.
	extension.CallInitiated(ctx)

	var timeout <-chan time.Time
	var timer *time.Timer
	// rpc-mode.ts:115-120: `if (opts?.timeout) setTimeout(...)` arms a Node timer for any truthy number, negative ones included.
	if hasTimeout && timeoutMs != 0 && !math.IsNaN(timeoutMs) {
		timer = time.NewTimer(extension.NodeTimerDelay(timeoutMs))
		timeout = timer.C
		defer timer.Stop()
	}

	defer u.remove(id, responses)
	select {
	case response := <-responses:
		if response.Cancelled {
			return response, context.Canceled
		}
		return response, nil
	case <-ctx.Done():
		return rpcUIResponse{}, ctx.Err()
	case <-timeout:
		// rpc-mode.ts:115-120 resolves the dialog's default on timeout: undefined for select and input, false for confirm, the same as a cancelled dialog.
		return rpcUIResponse{}, context.Canceled
	}
}

// ReportsDialogInitiation reports that Select, Confirm, Input, and Editor mark
// their call initiated once the request is written.
func (u *rpcUIContext) ReportsDialogInitiation() bool { return true }

// rpcDialogTimeout reads ExtensionUIDialogOptions.timeout as the raw JavaScript number the request echoes (rpc-mode.ts:138-150).
func rpcDialogTimeout(opts extension.ExtensionUIDialogOptions) (float64, bool) {
	if opts.Timeout == nil {
		return 0, false
	}
	return *opts.Timeout, true
}

func (u *rpcUIContext) remove(id string, responses chan rpcUIResponse) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pending[id] == responses {
		delete(u.pending, id)
		delete(u.published, id)
	}
}

func (u *rpcUIContext) HandleResponse(data []byte) bool {
	var response rpcUIResponse
	if json.Unmarshal(data, &response) != nil || response.Type != "extension_ui_response" || response.ID == "" {
		return false
	}
	u.mu.Lock()
	responses, ok := u.pending[response.ID]
	if ok {
		delete(u.pending, response.ID)
		delete(u.published, response.ID)
	}
	u.mu.Unlock()
	if !ok {
		return false
	}
	responses <- response
	return true
}

// PendingRequest returns a channel closed once a dialog waits for a response.
// After stdin ends no response can arrive, so such a dialog never resolves,
// as upstream leaves it pending until the process exits.
func (u *rpcUIContext) PendingRequest() <-chan struct{} {
	u.mu.Lock()
	defer u.mu.Unlock()
	waiter := make(chan struct{})
	if len(u.published) > 0 {
		close(waiter)
	} else {
		u.pendingWaiters = append(u.pendingWaiters, waiter)
	}
	return waiter
}

func (u *rpcUIContext) Close() {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.closed = true
	pending := u.pending
	u.pending = make(map[string]chan rpcUIResponse)
	clear(u.published)
	u.mu.Unlock()
	for _, responses := range pending {
		responses <- rpcUIResponse{Cancelled: true}
	}
}

func (u *rpcUIContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	response, err := u.request(ctx, rpcUIRequest{Method: RPCUIMethodSelect, Title: title, Options: options}, opts)
	if err != nil {
		return "", err
	}
	return response.Value, nil
}

func (u *rpcUIContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	response, err := u.request(ctx, rpcUIRequest{Method: RPCUIMethodConfirm, Title: title, Message: message}, opts)
	if err != nil {
		return false, err
	}
	return response.Confirmed, nil
}

func (u *rpcUIContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	request := rpcUIRequest{Method: RPCUIMethodInput, Title: title, Placeholder: &placeholder}
	response, err := u.request(ctx, request, opts)
	if err != nil {
		return "", err
	}
	return response.Value, nil
}

func (u *rpcUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	request := rpcUIRequest{Method: RPCUIMethodEditor, Title: title, Prefill: &prefill}
	response, err := u.request(ctx, request, extension.ExtensionUIDialogOptions{})
	if err != nil {
		return "", err
	}
	return response.Value, nil
}

func (u *rpcUIContext) emit(request rpcUIRequest) {
	request.Type = "extension_ui_request"
	request.ID = uuid.NewString()
	u.output(request)
}

func (u *rpcUIContext) Notify(message, kind string) {
	request := rpcUIRequest{Method: RPCUIMethodNotify, Message: message, NotifyType: kind}
	u.emit(request)
}

func (*rpcUIContext) OnTerminalInput(extension.TerminalInputHandler) func() { return func() {} }

func (*rpcUIContext) OnRemoteTerminalInput(string, extension.RemoteTerminalInputHandler) func() {
	return func() {}
}

func (u *rpcUIContext) SetStatus(key, text string) {
	request := rpcUIRequest{Method: RPCUIMethodSetStatus, StatusKey: key}
	if text != "" {
		request.StatusText = &text
	}
	u.emit(request)
}

func (*rpcUIContext) SetWorkingMessage(string)                              {}
func (*rpcUIContext) SetWorkingVisible(bool)                                {}
func (*rpcUIContext) SetWorkingIndicator(extension.WorkingIndicatorOptions) {}
func (*rpcUIContext) SetHiddenThinkingLabel(string)                         {}

// SetWidgetFactory ignores a component factory, which RPC mode cannot render, and removes the widget for a nil one: rpc-mode.ts:195-206 sends the
// widget request for `undefined` and for a string array only.
func (u *rpcUIContext) SetWidgetFactory(key string, factory extension.WidgetFactory, opts *extension.ExtensionWidgetOptions) {
	if factory == nil {
		u.SetWidget(key, nil, opts)
	}
}

func (u *rpcUIContext) SetWidget(key string, lines []string, opts *extension.ExtensionWidgetOptions) {
	request := rpcUIRequest{Method: RPCUIMethodSetWidget, WidgetKey: key}
	if lines != nil {
		request.WidgetLines = &lines
	}
	request.WidgetPlacement = rpcWidgetPlacement(opts)
	u.emit(request)
}

func rpcWidgetPlacement(opts *extension.ExtensionWidgetOptions) string {
	if opts == nil {
		return ""
	}
	return string(opts.Placement)
}

func (*rpcUIContext) SetFooter(extension.FooterFactory)        {}
func (*rpcUIContext) SetHeader(extension.HeaderFactory)        {}
func (*rpcUIContext) SetLogin(extension.LoginDefinition) error { return nil }

func (u *rpcUIContext) SetTitle(title string) {
	u.emit(rpcUIRequest{Method: RPCUIMethodSetTitle, Title: title})
}

func (*rpcUIContext) Custom(context.Context, extension.CustomFactory, *extension.CustomOptions) (any, error) {
	return nil, nil
}

func (u *rpcUIContext) PasteToEditor(text string) { u.SetEditorText(text) }

func (u *rpcUIContext) SetEditorText(text string) {
	u.emit(rpcUIRequest{Method: RPCUIMethodSetEditorText, Text: text})
}

func (*rpcUIContext) GetEditorText() string                                               { return "" }
func (*rpcUIContext) AddAutocompleteProvider(extension.AutocompleteProviderFactory) error { return nil }
func (*rpcUIContext) SetEditorComponent(extension.EditorFactory)                          {}
func (*rpcUIContext) GetEditorComponent() extension.EditorFactory                         { return nil }

// Theme is upstream rpc-mode.ts ui.theme, the active global theme.
func (*rpcUIContext) Theme() extension.Theme                   { return codingagent.ActiveExtensionTheme() }
func (*rpcUIContext) GetAllThemes() []extension.ThemeMeta      { return nil }
func (*rpcUIContext) GetTheme(string) (extension.Theme, error) { return nil, nil }
func (*rpcUIContext) SetTheme(extension.ThemeSelection) extension.SetThemeResult {
	return extension.SetThemeResult{Success: false, Error: "Theme switching not supported in RPC mode"}
}
func (*rpcUIContext) GetToolsExpanded() bool { return false }
func (*rpcUIContext) SetToolsExpanded(bool)  {}
func (*rpcUIContext) RunRemoteOverlay(extension.RemoteOverlayOptions, extension.RemoteOverlayHost, func(extension.RemoteOverlayHandle)) (any, bool) {
	return nil, false
}

var _ extension.UIContext = (*rpcUIContext)(nil)
