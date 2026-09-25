package extension_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestUIContext_AllUpstreamMethodsPresent locks the 26-method
// surface of `UIContext` against upstream `ExtensionUIContext`
// (types.ts:120-269). When upstream adds a new method, this test
// fires until the Go interface gains the matching signature.
//
// **Why a list-based gate (not a parser):** the upstream interface
// uses TS-only constructs (overloaded signatures, generics, readonly
// properties) that don't parse cleanly with the existing parity-gate
// machinery. Hand-listing keeps the sync ritual mechanical: when
// porting a new upstream version, diff types.ts:120-269 against this
// list.
//
// upstream: types.ts:120-269
func TestUIContext_AllUpstreamMethodsPresent(t *testing.T) {
	wantMethods := []string{
		"Select",                  // types.ts:124
		"Confirm",                 // types.ts:127
		"Input",                   // types.ts:130
		"Notify",                  // types.ts:133
		"OnTerminalInput",         // types.ts:136
		"SetStatus",               // types.ts:139
		"SetWorkingMessage",       // types.ts:142
		"SetWorkingVisible",       // v0.71.0 interactive/rpc UI contexts
		"SetWorkingIndicator",     // types.ts:153
		"SetHiddenThinkingLabel",  // types.ts:156
		"SetWidget",               // types.ts:159 (overload merged)
		"SetFooter",               // types.ts:172
		"SetHeader",               // types.ts:181
		"SetTitle",                // types.ts:184
		"Custom",                  // types.ts:187 (generic erased per D1)
		"PasteToEditor",           // types.ts:204
		"SetEditorText",           // types.ts:207
		"GetEditorText",           // types.ts:210
		"Editor",                  // types.ts:213
		"AddAutocompleteProvider", // types.ts:216
		"SetEditorComponent",      // types.ts:249
		"GetEditorComponent",      // v0.71.0 interactive/rpc UI contexts
		"Theme",                   // types.ts:254 (readonly → getter)
		"GetAllThemes",            // types.ts:257
		"GetTheme",                // types.ts:260
		"SetTheme",                // types.ts:262
		"GetToolsExpanded",        // types.ts:265
		"SetToolsExpanded",        // types.ts:268
	}

	// pig-specific extensions to UIContext. Each entry must reference
	// a documented divergence or a clearly pig-only feature with no
	// upstream counterpart. Adding new entries here without a comment
	// makes silent drift visible to reviewers.
	pigOnlyMethods := map[string]string{
		// RunRemoteOverlay backs ctx.ui.custom() for the TS
		// subprocess shim. Upstream's Custom() is single-method and
		// expects an in-process factory closure; the subprocess bridge
		// cannot serialise closures, so the shim adapter goes through
		// this protocol-shaped method instead. No upstream entry.
		"RunRemoteOverlay": "pig-specific: TS subprocess shim for ctx.ui.custom",
		"SetLogin":         "pig-specific: typed native login template",
		// OnRemoteTerminalInput registers a subprocess extension's
		// terminal-input listener, whose verdict crosses a socket and
		// therefore cannot be asked on the host's input loop.
		"OnRemoteTerminalInput": "pig-specific (D19): subprocess terminal-input listener",
	}

	uiType := reflect.TypeFor[extension.UIContext]()
	got := map[string]bool{}
	for method := range uiType.Methods() {
		got[method.Name] = true
	}

	for _, m := range wantMethods {
		if !got[m] {
			t.Errorf("UIContext.%s missing: upstream ExtensionUIContext expects it", m)
		}
	}

	// Inverse: catch pig-only methods accreting on the interface.
	want := map[string]bool{}
	for _, m := range wantMethods {
		want[m] = true
	}
	for m := range got {
		if want[m] {
			continue
		}
		if _, ok := pigOnlyMethods[m]; ok {
			continue
		}
		t.Errorf("UIContext.%s has no upstream counterpart: either remove "+
			"or extend wantMethods with a // upstream cite", m)
	}
}

// TestNoopUIContext_AllMethodsReturnUpstreamDefaults locks every
// stub-return value of NoopUIContext against the upstream
// `noOpUIContext` constant (runner.ts:188-217). Without this gate, a
// future worker who "improves" the noop (e.g. returns true from
// GetToolsExpanded by default) silently breaks fidelity.
func TestNoopUIContext_AllMethodsReturnUpstreamDefaults(t *testing.T) {
	ui := extension.NoopUIContext
	ctx := context.Background()

	t.Run("Select_returnsEmpty", func(t *testing.T) {
		got, err := ui.Select(ctx, "t", []string{"a"}, nil)
		if got != "" || err != nil {
			t.Errorf("Select = (%q, %v), want (\"\", nil): upstream: async () => undefined", got, err)
		}
	})
	t.Run("Confirm_returnsFalse", func(t *testing.T) {
		got, err := ui.Confirm(ctx, "t", "m", nil)
		if got || err != nil {
			t.Errorf("Confirm = (%v, %v), want (false, nil): upstream: async () => false", got, err)
		}
	})
	t.Run("Input_returnsEmpty", func(t *testing.T) {
		got, err := ui.Input(ctx, "t", "p", nil)
		if got != "" || err != nil {
			t.Errorf("Input = (%q, %v), want (\"\", nil)", got, err)
		}
	})
	t.Run("Editor_returnsEmpty", func(t *testing.T) {
		got, err := ui.Editor(ctx, "t", "p")
		if got != "" || err != nil {
			t.Errorf("Editor = (%q, %v), want (\"\", nil)", got, err)
		}
	})
	t.Run("Custom_returnsNil", func(t *testing.T) {
		got, err := ui.Custom(ctx, nil, nil)
		if got != nil || err != nil {
			t.Errorf("Custom = (%v, %v), want (nil, nil)", got, err)
		}
	})
	t.Run("OnTerminalInput_returnsUnsubscribe", func(t *testing.T) {
		unsub := ui.OnTerminalInput(func(string) extension.TerminalInputResult { return extension.TerminalInputResult{} })
		if unsub == nil {
			t.Error("OnTerminalInput returned nil unsubscribe; want callable noop")
		}
		unsub() // must not panic
	})
	t.Run("GetEditorText_returnsEmpty", func(t *testing.T) {
		if got := ui.GetEditorText(); got != "" {
			t.Errorf("GetEditorText = %q, want \"\"", got)
		}
	})
	t.Run("Theme_returnsNil", func(t *testing.T) {
		if got := ui.Theme(); got != nil {
			t.Errorf("Theme = %v, want nil", got)
		}
	})
	t.Run("GetAllThemes_returnsNil", func(t *testing.T) {
		if got := ui.GetAllThemes(); got != nil {
			t.Errorf("GetAllThemes = %v, want nil ([]ThemeMeta{})", got)
		}
	})
	t.Run("GetTheme_returnsNilNil", func(t *testing.T) {
		got, err := ui.GetTheme("dark")
		if got != nil || err != nil {
			t.Errorf("GetTheme = (%v, %v), want (nil, nil)", got, err)
		}
	})
	t.Run("SetTheme_returnsUpstreamFailure", func(t *testing.T) {
		got := ui.SetTheme("dark")
		if got.Success {
			t.Errorf("SetTheme.Success = true, want false")
		}
		// Upstream verbatim string: pattern-match fidelity.
		if got.Error != "UI not available" {
			t.Errorf("SetTheme.Error = %q, want %q (upstream verbatim)",
				got.Error, "UI not available")
		}
	})
	t.Run("GetToolsExpanded_returnsFalse", func(t *testing.T) {
		if ui.GetToolsExpanded() {
			t.Error("GetToolsExpanded = true, want false")
		}
	})

	// Sanity: no panics on the void methods.
	ui.Notify("hi", "info")
	ui.SetStatus("k", "v")
	ui.SetWorkingMessage("")
	ui.SetWorkingVisible(false)
	ui.SetWorkingIndicator(nil)
	ui.SetHiddenThinkingLabel("")
	ui.SetWidget("k", []string{"x"}, nil)
	ui.SetFooter(nil)
	ui.SetHeader(nil)
	if err := ui.SetLogin(extension.LoginDefinition{}); !errors.Is(err, extension.ErrUIUnavailable) {
		t.Errorf("SetLogin error = %v, want ErrUIUnavailable", err)
	}
	ui.SetTitle("title")
	ui.PasteToEditor("x")
	ui.SetEditorText("x")
	ui.AddAutocompleteProvider(nil)
	ui.SetEditorComponent(nil)
	ui.SetToolsExpanded(true)
}

// TestContext_UI_DefaultsToNoop locks that a Context with zero-value
// ContextActions returns NoopUIContext from UI(). Mirrors upstream's
// pre-bind state at runner.ts:255 (`this.uiContext = noOpUIContext`).
func TestContext_UI_DefaultsToNoop(t *testing.T) {
	c := extension.NewContext(".", nil, func() error { return nil }, extension.ContextActions{})

	ui, err := c.UI()
	if err != nil {
		t.Fatalf("UI err = %v", err)
	}
	if ui != extension.NoopUIContext {
		t.Errorf("UI() = %v, want NoopUIContext singleton", ui)
	}
}

// TestContext_HasUI_PointerIdentity locks upstream's pointer-identity
// check (runner.ts:361: `this.uiContext !== noOpUIContext`). HasUI
// is true iff a non-nil non-noop UI is wired.
func TestContext_HasUI_PointerIdentity(t *testing.T) {
	t.Run("nil_UI_normalizes_to_noop_reports_false", func(t *testing.T) {
		c := extension.NewContext(".", nil, func() error { return nil }, extension.ContextActions{})
		got, _ := c.HasUI()
		if got {
			t.Error("HasUI() = true, want false (nil UI normalizes to NoopUIContext)")
		}
	})
	t.Run("explicit_noop_reports_false", func(t *testing.T) {
		c := extension.NewContext(".", extension.NoopUIContext, func() error { return nil }, extension.ContextActions{})
		got, _ := c.HasUI()
		if got {
			t.Error("HasUI() = true, want false (explicit NoopUIContext)")
		}
	})
	t.Run("real_UI_reports_true", func(t *testing.T) {
		c := extension.NewContext(".", &fakeUIContext{}, func() error { return nil }, extension.ContextActions{})
		got, _ := c.HasUI()
		if !got {
			t.Error("HasUI() = false, want true (custom UIContext)")
		}
	})
}

// TestContext_UI_StaleErrors locks that UI() rejects stale runners
// before returning anything (assertActive-first).
func TestContext_UI_StaleErrors(t *testing.T) {
	c := extension.NewContext(".", &fakeUIContext{}, func() error { return extension.ErrStaleContext },
		extension.ContextActions{})
	if _, err := c.UI(); !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("UI() err = %v, want ErrStaleContext", err)
	}
}

// fakeUIContext is a minimal UIContext implementation for testing.
// All methods panic except where the test exercises them: proves
// HasUI's pointer-identity check uses the value, not a deep
// reflection-based equality.
type fakeUIContext struct{}

func (*fakeUIContext) Select(context.Context, string, []string, extension.ExtensionUIDialogOptions) (string, error) {
	panic("unreached")
}
func (*fakeUIContext) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	panic("unreached")
}
func (*fakeUIContext) Input(context.Context, string, string, extension.ExtensionUIDialogOptions) (string, error) {
	panic("unreached")
}
func (*fakeUIContext) Notify(string, string)                                         {}
func (*fakeUIContext) OnTerminalInput(extension.TerminalInputHandler) func()         { return func() {} }
func (*fakeUIContext) SetStatus(string, string)                                      {}
func (*fakeUIContext) SetWorkingMessage(string)                                      {}
func (*fakeUIContext) SetWorkingVisible(bool)                                        {}
func (*fakeUIContext) SetWorkingIndicator(extension.WorkingIndicatorOptions)         {}
func (*fakeUIContext) SetHiddenThinkingLabel(string)                                 {}
func (*fakeUIContext) SetWidget(string, any, extension.ExtensionWidgetOptions)       {}
func (*fakeUIContext) SetFooter(any)                                                 {}
func (*fakeUIContext) SetHeader(any)                                                 {}
func (*fakeUIContext) SetLogin(extension.LoginDefinition) error                      { return nil }
func (*fakeUIContext) SetTitle(string)                                               {}
func (*fakeUIContext) Custom(context.Context, any, any) (any, error)                 { return nil, nil }
func (*fakeUIContext) PasteToEditor(string)                                          {}
func (*fakeUIContext) SetEditorText(string)                                          {}
func (*fakeUIContext) GetEditorText() string                                         { return "" }
func (*fakeUIContext) Editor(context.Context, string, string) (string, error)        { return "", nil }
func (*fakeUIContext) AddAutocompleteProvider(extension.AutocompleteProviderFactory) {}
func (*fakeUIContext) SetEditorComponent(any)                                        {}
func (*fakeUIContext) GetEditorComponent() any                                       { return nil }
func (*fakeUIContext) Theme() extension.Theme                                        { return nil }
func (*fakeUIContext) GetAllThemes() []extension.ThemeMeta                           { return nil }
func (*fakeUIContext) GetTheme(string) (extension.Theme, error)                      { return nil, nil }
func (*fakeUIContext) SetTheme(any) extension.SetThemeResult {
	return extension.SetThemeResult{Success: true}
}
func (*fakeUIContext) GetToolsExpanded() bool { return false }
func (*fakeUIContext) SetToolsExpanded(bool)  {}
func (*fakeUIContext) RunRemoteOverlay(extension.RemoteOverlayOptions, extension.RemoteOverlayHost, func(extension.RemoteOverlayHandle)) (any, bool) {
	return nil, false
}
func (*fakeUIContext) OnRemoteTerminalInput(string, extension.RemoteTerminalInputHandler) func() {
	return func() {}
}
