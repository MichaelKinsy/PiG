package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type rpcUIResponse struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Value     string `json:"value,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

type rpcUIRequest struct {
	Type            string    `json:"type"`
	ID              string    `json:"id"`
	Method          string    `json:"method"`
	Title           string    `json:"title,omitempty"`
	Options         []string  `json:"options,omitempty"`
	Message         string    `json:"message,omitempty"`
	Placeholder     *string   `json:"placeholder,omitempty"`
	Prefill         *string   `json:"prefill,omitempty"`
	Timeout         *int64    `json:"timeout,omitempty"`
	NotifyType      string    `json:"notifyType,omitempty"`
	StatusKey       string    `json:"statusKey,omitempty"`
	StatusText      *string   `json:"statusText,omitempty"`
	WidgetKey       string    `json:"widgetKey,omitempty"`
	WidgetLines     *[]string `json:"widgetLines,omitempty"`
	WidgetPlacement string    `json:"widgetPlacement,omitempty"`
	Text            string    `json:"text,omitempty"`
}

type rpcUIContext struct {
	output func(any)

	mu      sync.Mutex
	pending map[string]chan rpcUIResponse
	closed  bool
}

func newRPCUIContext(output func(any)) *rpcUIContext {
	return &rpcUIContext{output: output, pending: make(map[string]chan rpcUIResponse)}
}

func (u *rpcUIContext) request(ctx context.Context, request rpcUIRequest, opts extension.ExtensionUIDialogOptions) (rpcUIResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := uuid.NewString()
	request.Type = "extension_ui_request"
	request.ID = id
	if timeout := rpcDialogTimeout(opts); timeout > 0 {
		milliseconds := timeout.Milliseconds()
		request.Timeout = &milliseconds
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

	var timeout <-chan time.Time
	var timer *time.Timer
	if duration := rpcDialogTimeout(opts); duration > 0 {
		timer = time.NewTimer(duration)
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
		return rpcUIResponse{}, nil
	}
}

func rpcDialogTimeout(opts extension.ExtensionUIDialogOptions) time.Duration {
	if opts == nil {
		return 0
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return 0
	}
	var value struct {
		Timeout float64 `json:"timeout"`
	}
	if json.Unmarshal(data, &value) != nil || value.Timeout <= 0 {
		return 0
	}
	return time.Duration(value.Timeout * float64(time.Millisecond))
}

func (u *rpcUIContext) remove(id string, responses chan rpcUIResponse) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.pending[id] == responses {
		delete(u.pending, id)
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
	}
	u.mu.Unlock()
	if !ok {
		return false
	}
	responses <- response
	return true
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
	u.mu.Unlock()
	for _, responses := range pending {
		responses <- rpcUIResponse{Cancelled: true}
	}
}

func (u *rpcUIContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	response, err := u.request(ctx, rpcUIRequest{Method: "select", Title: title, Options: options}, opts)
	if err != nil {
		return "", err
	}
	return response.Value, nil
}

func (u *rpcUIContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	response, err := u.request(ctx, rpcUIRequest{Method: "confirm", Title: title, Message: message}, opts)
	if err != nil {
		return false, err
	}
	return response.Confirmed, nil
}

func (u *rpcUIContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	request := rpcUIRequest{Method: "input", Title: title, Placeholder: &placeholder}
	response, err := u.request(ctx, request, opts)
	if err != nil {
		return "", err
	}
	return response.Value, nil
}

func (u *rpcUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	request := rpcUIRequest{Method: "editor", Title: title, Prefill: &prefill}
	response, err := u.request(ctx, request, nil)
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
	request := rpcUIRequest{Method: "notify", Message: message, NotifyType: kind}
	u.emit(request)
}

func (*rpcUIContext) OnTerminalInput(extension.TerminalInputHandler) func() { return func() {} }

func (*rpcUIContext) OnRemoteTerminalInput(string, extension.RemoteTerminalInputHandler) func() {
	return func() {}
}

func (u *rpcUIContext) SetStatus(key, text string) {
	request := rpcUIRequest{Method: "setStatus", StatusKey: key}
	if text != "" {
		request.StatusText = &text
	}
	u.emit(request)
}

func (*rpcUIContext) SetWorkingMessage(string)                              {}
func (*rpcUIContext) SetWorkingVisible(bool)                                {}
func (*rpcUIContext) SetWorkingIndicator(extension.WorkingIndicatorOptions) {}
func (*rpcUIContext) SetHiddenThinkingLabel(string)                         {}

func (u *rpcUIContext) SetWidget(key string, content any, opts extension.ExtensionWidgetOptions) {
	lines, ok := content.([]string)
	if content != nil && !ok {
		return
	}
	request := rpcUIRequest{Method: "setWidget", WidgetKey: key}
	if lines != nil {
		request.WidgetLines = &lines
	}
	request.WidgetPlacement = rpcWidgetPlacement(opts)
	u.emit(request)
}

func rpcWidgetPlacement(opts extension.ExtensionWidgetOptions) string {
	if opts == nil {
		return ""
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return ""
	}
	var value struct {
		Placement string `json:"placement"`
	}
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	return value.Placement
}

func (*rpcUIContext) SetFooter(any)                            {}
func (*rpcUIContext) SetHeader(any)                            {}
func (*rpcUIContext) SetLogin(extension.LoginDefinition) error { return nil }

func (u *rpcUIContext) SetTitle(title string) {
	u.emit(rpcUIRequest{Method: "setTitle", Title: title})
}

func (*rpcUIContext) Custom(context.Context, any, any) (any, error) { return nil, nil }

func (u *rpcUIContext) PasteToEditor(text string) { u.SetEditorText(text) }

func (u *rpcUIContext) SetEditorText(text string) {
	u.emit(rpcUIRequest{Method: "set_editor_text", Text: text})
}

func (*rpcUIContext) GetEditorText() string                                         { return "" }
func (*rpcUIContext) AddAutocompleteProvider(extension.AutocompleteProviderFactory) {}
func (*rpcUIContext) SetEditorComponent(any)                                        {}
func (*rpcUIContext) GetEditorComponent() any                                       { return nil }
func (*rpcUIContext) Theme() extension.Theme                                        { return nil }
func (*rpcUIContext) GetAllThemes() []extension.ThemeMeta                           { return nil }
func (*rpcUIContext) GetTheme(string) (extension.Theme, error)                      { return nil, nil }
func (*rpcUIContext) SetTheme(any) extension.SetThemeResult {
	return extension.SetThemeResult{Success: false, Error: "Theme switching not supported in RPC mode"}
}
func (*rpcUIContext) GetToolsExpanded() bool { return false }
func (*rpcUIContext) SetToolsExpanded(bool)  {}
func (*rpcUIContext) RunRemoteOverlay(extension.RemoteOverlayOptions, extension.RemoteOverlayHost, func(extension.RemoteOverlayHandle)) (any, bool) {
	return nil, false
}

var _ extension.UIContext = (*rpcUIContext)(nil)
