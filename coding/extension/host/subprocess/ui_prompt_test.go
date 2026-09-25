package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

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

func (s *recordingPromptScope) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if calls := s.snapshot(); len(calls) >= n {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("prompt scope calls = %v, want %d", s.snapshot(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// blockingSelectUI answers select only when released; confirm answers at
// once.
type blockingSelectUI struct {
	extension.UIContext
	entered chan struct{}
	release chan struct{}
}

func (u *blockingSelectUI) Select(ctx context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.entered <- struct{}{}
	select {
	case <-u.release:
		return "first", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (u *blockingSelectUI) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	return true, nil
}

func dialogCall(method string, args map[string]any) *CallPayload {
	raw, _ := json.Marshal(args)
	return &CallPayload{Method: method, Args: raw}
}

// A dialog that waits for another dialog's terminal focus is already a
// prompt the extension waits on: the scope opens on arrival, not on focus.
func TestUIBridgePromptScopeOpensBeforeFocusWait(t *testing.T) {
	ui := &blockingSelectUI{UIContext: extension.NoopUIContext, entered: make(chan struct{}, 1), release: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	scope := &recordingPromptScope{}
	bridge.SetUIPromptScope(scope)

	first := make(chan error, 1)
	go func() {
		_, err := bridge.HandleCall("ext", dialogCall("ui.select", map[string]any{"title": "First", "options": []string{"a"}}))
		first <- err
	}()
	<-ui.entered
	second := make(chan error, 1)
	go func() {
		_, err := bridge.HandleCall("ext", dialogCall("ui.confirm", map[string]any{"title": "Second", "message": "m"}))
		second <- err
	}()
	if got := scope.waitFor(t, 2); !slices.Equal(got, []string{"begin:select:First", "begin:confirm:Second"}) {
		t.Fatalf("scope calls while second waits for focus = %v", got)
	}
	close(ui.release)
	for _, done := range []chan error{first, second} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"begin:select:First", "begin:confirm:Second", "end:select:First", "end:confirm:Second"}
	if got := scope.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("scope calls = %v, want %v", got, want)
	}
}

// Cancellation while waiting for focus closes the scope; custom has no
// title; non-dialog calls and a missing UI surface open nothing.
func TestUIBridgePromptScopeCancellationCustomAndNoUI(t *testing.T) {
	ui := &blockingSelectUI{UIContext: extension.NoopUIContext, entered: make(chan struct{}, 1), release: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	scope := &recordingPromptScope{}
	bridge.SetUIPromptScope(scope)

	go func() {
		_, _ = bridge.HandleCall("ext", dialogCall("ui.select", map[string]any{"title": "Holder"}))
	}()
	<-ui.entered
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan *CallResultPayload, 1)
	go func() {
		result, _ := bridge.handleCall(ctx, "ext", nil, dialogCall("ui.input", map[string]any{"title": "Queued"}))
		waiting <- result
	}()
	scope.waitFor(t, 2)
	cancel()
	if result := <-waiting; result == nil || result.Error == nil || result.Error.Code != "cancelled" {
		t.Fatalf("queued input result = %+v", result)
	}
	close(ui.release)
	scope.waitFor(t, 4)

	_, _ = bridge.HandleCall("ext", dialogCall(CallUICustom, map[string]any{"title": "Overlay"}))
	_, _ = bridge.HandleCall("ext", dialogCall("ui.notify", map[string]any{"message": "hi"}))
	bridge.SetUIContext(nil)
	_, _ = bridge.HandleCall("ext", dialogCall("ui.editor", map[string]any{"title": "Headless"}))

	want := []string{
		"begin:select:Holder", "begin:input:Queued", "end:input:Queued", "end:select:Holder",
		"begin:custom:", "end:custom:",
	}
	if got := scope.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("scope calls = %v, want %v", got, want)
	}
}
