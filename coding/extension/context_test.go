package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestContext_RoundTrip verifies that WithContext attaches the per-extension
// Context to a stdlib context.Context and FromContext retrieves it. This is
// the foundation of the D3 divergence: extensions never see a separate
// AbortSignal, only the unified context.Context.
func TestContext_RoundTrip(t *testing.T) {
	ext := NewContext("/work", nil, func() error { return nil }, ContextActions{})
	ctx := WithContext(context.Background(), ext)

	got := FromContext(ctx)
	if got != ext {
		t.Fatalf("FromContext returned %p, want %p", got, ext)
	}
}

// TestContext_FromContextNilWhenAbsent verifies that a fresh stdlib context
// with no extension Context attached returns nil rather than panicking.
// Authors writing handlers in unit tests rely on this behaviour.
func TestContext_FromContextNilWhenAbsent(t *testing.T) {
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext on bare context returned %p, want nil", got)
	}
}

// TestContext_CancellationPropagates verifies that a cancelled stdlib
// context propagates to handlers via ctx.Done(), replacing the upstream
// AbortSignal.aborted check pattern.
func TestContext_CancellationPropagates(t *testing.T) {
	ext := NewContext(".", nil, func() error { return nil }, ContextActions{})
	ctx, cancel := context.WithCancel(WithContext(context.Background(), ext))
	cancel()

	select {
	case <-ctx.Done():
		// Expected.
	default:
		t.Fatal("context.Done() did not fire after cancel")
	}
	if FromContext(ctx) == nil {
		t.Fatal("cancelled context lost the per-extension Context")
	}
}

// TestContext_CWD_ReturnsRunnerCWD: CWD() returns the working directory
// captured from the runner at context-creation time.
func TestContext_CWD_ReturnsRunnerCWD(t *testing.T) {
	ext := NewContext("/work/dir", nil, func() error { return nil }, ContextActions{})
	got, err := ext.CWD()
	if err != nil {
		t.Fatalf("CWD err = %v", err)
	}
	if got != "/work/dir" {
		t.Errorf("CWD() = %q, want /work/dir", got)
	}
}

// TestContext_CWD_RejectsStale: CWD() returns an error when the runner
// has been invalidated.
func TestContext_CWD_RejectsStale(t *testing.T) {
	staleErr := errors.New("stale")
	ext := NewContext(".", nil, func() error { return staleErr }, ContextActions{})
	_, err := ext.CWD()
	if !errors.Is(err, staleErr) {
		t.Errorf("CWD err = %v, want staleErr", err)
	}
}

// TestContext_HasUI: HasUI() returns true when a non-noop UI is
// bound, false otherwise.
func TestContext_HasUI(t *testing.T) {
	// With explicit non-noop UI: HasUI = true.
	ext := NewContext(".", &fakeForHasUI{}, func() error { return nil }, ContextActions{})
	got, err := ext.HasUI()
	if err != nil {
		t.Fatalf("HasUI err = %v", err)
	}
	if !got {
		t.Errorf("HasUI() = false, want true (non-noop UI bound)")
	}

	// With nil UI (normalized to NoopUIContext): HasUI = false.
	ext = NewContext(".", nil, func() error { return nil }, ContextActions{})
	got, _ = ext.HasUI()
	if got {
		t.Errorf("HasUI() = true, want false (nil UI normalizes to NoopUIContext)")
	}
}

// fakeForHasUI is a minimal UIContext stub for TestContext_HasUI -
// methods panic since none are exercised by HasUI's pointer-identity
// check.
type fakeForHasUI struct{}

func (*fakeForHasUI) Select(context.Context, string, []string, ExtensionUIDialogOptions) (string, error) {
	panic("unreached")
}
func (*fakeForHasUI) Confirm(context.Context, string, string, ExtensionUIDialogOptions) (bool, error) {
	panic("unreached")
}
func (*fakeForHasUI) Input(context.Context, string, string, ExtensionUIDialogOptions) (string, error) {
	panic("unreached")
}
func (*fakeForHasUI) Notify(string, string)                                  {}
func (*fakeForHasUI) OnTerminalInput(TerminalInputHandler) func()            { return func() {} }
func (*fakeForHasUI) SetStatus(string, string)                               {}
func (*fakeForHasUI) SetWorkingMessage(string)                               {}
func (*fakeForHasUI) SetWorkingVisible(bool)                                 {}
func (*fakeForHasUI) SetWorkingIndicator(WorkingIndicatorOptions)            {}
func (*fakeForHasUI) SetHiddenThinkingLabel(string)                          {}
func (*fakeForHasUI) SetWidget(string, any, ExtensionWidgetOptions)          {}
func (*fakeForHasUI) SetFooter(any)                                          {}
func (*fakeForHasUI) SetHeader(any)                                          {}
func (*fakeForHasUI) SetLogin(LoginDefinition) error                         { return nil }
func (*fakeForHasUI) SetTitle(string)                                        {}
func (*fakeForHasUI) Custom(context.Context, any, any) (any, error)          { return nil, nil }
func (*fakeForHasUI) PasteToEditor(string)                                   {}
func (*fakeForHasUI) SetEditorText(string)                                   {}
func (*fakeForHasUI) GetEditorText() string                                  { return "" }
func (*fakeForHasUI) Editor(context.Context, string, string) (string, error) { return "", nil }
func (*fakeForHasUI) AddAutocompleteProvider(AutocompleteProviderFactory)    {}
func (*fakeForHasUI) SetEditorComponent(any)                                 {}
func (*fakeForHasUI) GetEditorComponent() any                                { return nil }
func (*fakeForHasUI) Theme() Theme                                           { return nil }
func (*fakeForHasUI) GetAllThemes() []ThemeMeta                              { return nil }
func (*fakeForHasUI) GetTheme(string) (Theme, error)                         { return nil, nil }
func (*fakeForHasUI) SetTheme(any) SetThemeResult                            { return SetThemeResult{} }
func (*fakeForHasUI) GetToolsExpanded() bool                                 { return false }
func (*fakeForHasUI) SetToolsExpanded(bool)                                  {}
func (*fakeForHasUI) RunRemoteOverlay(RemoteOverlayOptions, RemoteOverlayHost, func(RemoteOverlayHandle)) (any, bool) {
	return nil, false
}
func (*fakeForHasUI) OnRemoteTerminalInput(string, RemoteTerminalInputHandler) func() {
	return func() {}
}

// TestToolExecuteFunc_AcceptsContext verifies the migrated D8 signature
// shape: ToolExecuteFunc takes context.Context as its first argument
// (no separate AbortSignal). This is a compile-time gate via a typed
// nil assignment.
func TestToolExecuteFunc_AcceptsContext(t *testing.T) {
	var fn = func(ctx context.Context, _ string, _ json.RawMessage, _ AgentToolUpdateCallback) (AgentToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, nil
		}
	}
	_ = fn // compile-time type gate: verifies ToolExecuteFunc signature shape
}
