package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// rpcUIResponse is an extension_ui_response as Pi reads it: `value` and `confirmed` are whatever JSON the client sent (rpc-mode.ts:137-150 `"value" in r ? r.value : undefined`,
// `"confirmed" in r ? r.confirmed : false`), so they stay raw; an absent member is nil. `cancelled` is read by JavaScript truthiness (`"cancelled" in r && r.cancelled`).
type rpcUIResponse struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Value     json.RawMessage `json:"value,omitempty"`
	Confirmed json.RawMessage `json:"confirmed,omitempty"`
	Cancelled json.RawMessage `json:"cancelled,omitempty"`
}

// rpcCancelled reports JavaScript truthiness of the `cancelled` member: absent, null, false, 0 and "" are falsy; every other JSON value, [] and {} included, is truthy.
func (r rpcUIResponse) cancelled() bool {
	raw := bytes.TrimSpace(r.Cancelled)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n', 'f':
		return false
	case '"':
		return string(raw) != `""`
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		number, _ := strconv.ParseFloat(string(raw), 64)
		return number != 0
	}
	return true
}

// dialogValue reads the value of a select, input or editor response. rpc-mode.ts:138-150 and 254-266 resolve `"value" in r ? r.value : undefined`:
// a response without a value resolves undefined, which these dialogs report as context.Canceled, the same as a cancelled or timed-out dialog.
// A present value is returned as the client's JSON, `null` and non-strings included.
func (r rpcUIResponse) dialogValue() (json.RawMessage, error) {
	if r.Value == nil {
		return nil, context.Canceled
	}
	return r.Value, nil
}

// stringValue is dialogValue for the string-typed [extension.UIContext] methods. A value that is not a JSON string cannot be returned as a Go string;
// the dialog reports cancelled (undefined), and the SDK wire carries the value through the [extension.DialogValueUIContext] methods instead.
func (r rpcUIResponse) stringValue() (string, error) {
	raw, err := r.dialogValue()
	if err != nil {
		return "", err
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", context.Canceled
	}
	return text, nil
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

// rpcUIRequest is the RpcExtensionUIRequest Pi writes (rpc-types.ts). Pi serialises a literal with JSON.stringify, which writes an empty string, an empty array
// and 0 and omits only `undefined`, so a member that Pi can write empty is a pointer here: nil is `undefined` and omitted, anything else is written.
type rpcUIRequest struct {
	Type            string      `json:"type"`
	ID              string      `json:"id"`
	Method          RPCUIMethod `json:"method"`
	Title           *string     `json:"title,omitempty"`
	Options         *[]string   `json:"options,omitempty"`
	Message         *string     `json:"message,omitempty"`
	Placeholder     *string     `json:"placeholder,omitempty"`
	Prefill         *string     `json:"prefill,omitempty"`
	Timeout         *float64    `json:"timeout,omitempty"`
	NotifyType      string      `json:"notifyType,omitempty"`
	StatusKey       *string     `json:"statusKey,omitempty"`
	StatusText      *string     `json:"statusText,omitempty"`
	WidgetKey       *string     `json:"widgetKey,omitempty"`
	WidgetLines     *[]string   `json:"widgetLines,omitempty"`
	WidgetPlacement string      `json:"widgetPlacement,omitempty"`
	Text            *string     `json:"text,omitempty"`
}

type rpcUIContext struct {
	output func(any)

	mu        sync.Mutex
	pending   map[string]chan rpcUIResponse
	published map[string]struct{}
	// settle runs when a dialog's response is read from stdin (extension.WithDialogSettle), keyed like pending.
	settle map[string]func()
	closed bool
	// pendingWaiters are closed when a dialog starts waiting for a response.
	pendingWaiters []chan struct{}
}

func newRPCUIContext(output func(any)) *rpcUIContext {
	return &rpcUIContext{output: output, pending: make(map[string]chan rpcUIResponse), published: make(map[string]struct{}), settle: make(map[string]func())}
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
	if settle := extension.DialogSettleFromContext(ctx); settle != nil {
		u.settle[id] = settle
	}
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
		if response.cancelled() {
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
		delete(u.settle, id)
	}
}

func (u *rpcUIContext) HandleResponse(data []byte) bool {
	var response rpcUIResponse
	if json.Unmarshal(data, &response) != nil || response.Type != "extension_ui_response" || response.ID == "" {
		return false
	}
	u.mu.Lock()
	responses, ok := u.pending[response.ID]
	settle := u.settle[response.ID]
	if ok {
		delete(u.pending, response.ID)
		delete(u.published, response.ID)
		delete(u.settle, response.ID)
	}
	u.mu.Unlock()
	if !ok {
		return false
	}
	// Pi resolves the dialog on this stdin line: the answers' order is the order of the lines, which the goroutines waiting in the dialogs do not keep.
	if settle != nil {
		settle()
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
	clear(u.settle)
	u.mu.Unlock()
	for _, responses := range pending {
		responses <- rpcUIResponse{Cancelled: json.RawMessage("true")}
	}
}

func selectRequest(title string, options []string) rpcUIRequest {
	request := rpcUIRequest{Method: RPCUIMethodSelect, Title: &title}
	// A nil options is `undefined` (omitted); an empty list is written as [].
	if options != nil {
		request.Options = &options
	}
	return request
}

func confirmRequest(title, message string) rpcUIRequest {
	return rpcUIRequest{Method: RPCUIMethodConfirm, Title: &title, Message: &message}
}

func inputRequest(title string, placeholder *string) rpcUIRequest {
	return rpcUIRequest{Method: RPCUIMethodInput, Title: &title, Placeholder: placeholder}
}

func editorRequest(title string, prefill *string) rpcUIRequest {
	return rpcUIRequest{Method: RPCUIMethodEditor, Title: &title, Prefill: prefill}
}

func (u *rpcUIContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	return u.stringDialog(ctx, selectRequest(title, options), opts)
}

func (u *rpcUIContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	response, err := u.request(ctx, confirmRequest(title, message), opts)
	if err != nil {
		return false, err
	}
	return response.confirmedBool(), nil
}

// Input writes the placeholder as given, the empty string included; [rpcUIContext.InputWithoutPlaceholder] is Pi's undefined placeholder.
func (u *rpcUIContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	return u.stringDialog(ctx, inputRequest(title, &placeholder), opts)
}

// InputWithoutPlaceholder opens an input dialog whose request has no placeholder member ([extension.PlaceholderOmitter]).
func (u *rpcUIContext) InputWithoutPlaceholder(ctx context.Context, title string, opts extension.ExtensionUIDialogOptions) (string, error) {
	return u.stringDialog(ctx, inputRequest(title, nil), opts)
}

func (u *rpcUIContext) stringDialog(ctx context.Context, request rpcUIRequest, opts extension.ExtensionUIDialogOptions) (string, error) {
	response, err := u.request(ctx, request, opts)
	if err != nil {
		return "", err
	}
	return response.stringValue()
}

// Editor writes the prefill as given, the empty string included; [rpcUIContext.EditorWithoutPrefill] is Pi's undefined prefill.
func (u *rpcUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	return u.stringDialog(ctx, editorRequest(title, &prefill), extension.ExtensionUIDialogOptions{})
}

// EditorWithoutPrefill opens an editor dialog whose request has no prefill member ([extension.PrefillOmitter]).
func (u *rpcUIContext) EditorWithoutPrefill(ctx context.Context, title string) (string, error) {
	return u.stringDialog(ctx, editorRequest(title, nil), extension.ExtensionUIDialogOptions{})
}

// confirmedBool is the string-typed Confirm result: true only for the JSON value `true`, as a Go bool cannot hold another client value.
func (r rpcUIResponse) confirmedBool() bool { return string(bytes.TrimSpace(r.Confirmed)) == "true" }

// confirmedValue is the `"confirmed" in r ? r.confirmed : false` of rpc-mode.ts:138-150.
func (r rpcUIResponse) confirmedValue() json.RawMessage {
	if r.Confirmed == nil {
		return json.RawMessage("false")
	}
	return r.Confirmed
}

func (u *rpcUIContext) valueDialog(ctx context.Context, request rpcUIRequest, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	response, err := u.request(ctx, request, opts)
	if err != nil {
		return nil, err
	}
	return response.dialogValue()
}

// SelectValue, ConfirmValue, InputValue and EditorValue are [extension.DialogValueUIContext]: the dialog resolves with the JSON the client sent, as Pi's does.
// A nil placeholder or prefill is Pi's undefined: the request has no such member.
func (u *rpcUIContext) SelectValue(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.valueDialog(ctx, selectRequest(title, options), opts)
}

func (u *rpcUIContext) ConfirmValue(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	response, err := u.request(ctx, confirmRequest(title, message), opts)
	if err != nil {
		return nil, err
	}
	return response.confirmedValue(), nil
}

func (u *rpcUIContext) InputValue(ctx context.Context, title string, placeholder *string, opts extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.valueDialog(ctx, inputRequest(title, placeholder), opts)
}

func (u *rpcUIContext) EditorValue(ctx context.Context, title string, prefill *string) (json.RawMessage, error) {
	return u.valueDialog(ctx, editorRequest(title, prefill), extension.ExtensionUIDialogOptions{})
}

func (u *rpcUIContext) emit(request rpcUIRequest) {
	request.Type = "extension_ui_request"
	request.ID = uuid.NewString()
	u.output(request)
}

func (u *rpcUIContext) Notify(message, kind string) {
	request := rpcUIRequest{Method: RPCUIMethodNotify, Message: &message, NotifyType: kind}
	u.emit(request)
}

func (*rpcUIContext) OnTerminalInput(extension.TerminalInputHandler) func() { return func() {} }

func (*rpcUIContext) OnRemoteTerminalInput(string, extension.RemoteTerminalInputHandler) func() {
	return func() {}
}

// SetStatus writes the text as given, the empty string included: rpc-mode.ts setStatus writes `statusText: text`, and only `undefined` omits it
// ([rpcUIContext.ClearStatus]).
func (u *rpcUIContext) SetStatus(key, text string) {
	u.emit(rpcUIRequest{Method: RPCUIMethodSetStatus, StatusKey: &key, StatusText: &text})
}

// ClearStatus is setStatus(key, undefined): the request has no statusText ([extension.StatusClearer]).
func (u *rpcUIContext) ClearStatus(key string) {
	u.emit(rpcUIRequest{Method: RPCUIMethodSetStatus, StatusKey: &key})
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
	request := rpcUIRequest{Method: RPCUIMethodSetWidget, WidgetKey: &key}
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
	u.emit(rpcUIRequest{Method: RPCUIMethodSetTitle, Title: &title})
}

func (*rpcUIContext) Custom(context.Context, extension.CustomFactory, *extension.CustomOptions) (any, error) {
	return nil, nil
}

func (u *rpcUIContext) PasteToEditor(text string) { u.SetEditorText(text) }

func (u *rpcUIContext) SetEditorText(text string) {
	u.emit(rpcUIRequest{Method: RPCUIMethodSetEditorText, Text: &text})
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
