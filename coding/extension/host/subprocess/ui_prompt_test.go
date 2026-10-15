package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// recordingPromptScope records the order of prompt scope openings and
// closings as the bridge reports them.
type recordingPromptScope struct {
	mu    sync.Mutex
	calls []string
}

func (s *recordingPromptScope) BeginUIPrompt(kind extension.UIPromptKind, title string) func() {
	s.record(fmt.Sprintf("begin:%s:%s", kind, title))
	return func() { s.record(fmt.Sprintf("end:%s:%s", kind, title)) }
}

func (s *recordingPromptScope) AwaitUIPromptEvents(context.Context) {}

func (s *recordingPromptScope) record(line string) {
	s.mu.Lock()
	s.calls = append(s.calls, line)
	s.mu.Unlock()
}

func (s *recordingPromptScope) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// blockingCustomUI holds a custom component open until released; confirm
// answers at once.
type blockingCustomUI struct {
	extension.UIContext
	entered chan struct{}
	release chan struct{}
}

func (u *blockingCustomUI) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	return true, nil
}

// RunRemoteOverlay holds a focused custom component open until released.
func (u *blockingCustomUI) RunRemoteOverlay(_ extension.RemoteOverlayOptions, _ extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	onHandle(&capturingRemoteOverlayHandle{})
	u.entered <- struct{}{}
	<-u.release
	return nil, true
}

func dialogCall(method string, args map[string]any) *CallPayload {
	raw, _ := json.Marshal(args)
	return &CallPayload{Method: method, Args: raw}
}

// A dialog over a focused custom component opens its own scope and closes it
// when it returns, while the component's scope stays open; custom has no
// title; non-dialog calls and a missing UI surface open nothing.
func TestUIBridgePromptScopeDialogOverCustomAndNoUI(t *testing.T) {
	ui := &blockingCustomUI{UIContext: extension.NoopUIContext, entered: make(chan struct{}, 1), release: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	scope := &recordingPromptScope{}
	bridge.SetUIPromptScope(scope)

	holder := make(chan error, 1)
	go func() {
		_, err := bridge.HandleCall("ext", dialogCall(CallUICustom, map[string]any{"key": "holder"}))
		holder <- err
	}()
	<-ui.entered
	if _, err := bridge.HandleCall("ext", dialogCall("ui.confirm", map[string]any{"title": "Over", "message": "m"})); err != nil {
		t.Fatal(err)
	}
	if got := scope.snapshot(); !slices.Equal(got, []string{"begin:custom:", "begin:confirm:Over", "end:confirm:Over"}) {
		t.Fatalf("scope calls while the custom component is shown = %v", got)
	}
	close(ui.release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}

	_, _ = bridge.HandleCall("ext", dialogCall(CallUICustom, map[string]any{"title": "Overlay"}))
	_, _ = bridge.HandleCall("ext", dialogCall("ui.notify", map[string]any{"message": "hi"}))
	bridge.SetUIContext(nil)
	_, _ = bridge.HandleCall("ext", dialogCall("ui.editor", map[string]any{"title": "Headless"}))

	want := []string{
		"begin:custom:", "begin:confirm:Over", "end:confirm:Over", "end:custom:",
		"begin:custom:", "end:custom:",
	}
	if got := scope.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("scope calls = %v, want %v", got, want)
	}
}
