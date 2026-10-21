package inproc_test

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// unsetUI implements the optional interfaces that tell Pi's undefined from an empty string, as the RPC UI does.
type unsetUI struct {
	extension.UIContext
	calls []string
}

func (u *unsetUI) SetStatus(key, text string) { u.calls = append(u.calls, "status:"+key+":"+text) }
func (u *unsetUI) ClearStatus(key string)     { u.calls = append(u.calls, "status:"+key+":<unset>") }
func (u *unsetUI) Input(_ context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.calls = append(u.calls, "input:"+title+":"+placeholder)
	return "", nil
}
func (u *unsetUI) InputWithoutPlaceholder(_ context.Context, title string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.calls = append(u.calls, "input:"+title+":<unset>")
	return "", nil
}
func (u *unsetUI) Editor(_ context.Context, title, prefill string) (string, error) {
	u.calls = append(u.calls, "editor:"+title+":"+prefill)
	return "", nil
}
func (u *unsetUI) EditorWithoutPrefill(_ context.Context, title string) (string, error) {
	u.calls = append(u.calls, "editor:"+title+":<unset>")
	return "", nil
}

// plainUnsetUI forwards the string forms only, as a UIContext written before the optional interfaces existed does.
type plainUnsetUI struct {
	extension.UIContext
	calls []string
}

func (u *plainUnsetUI) SetStatus(key, text string) { u.calls = append(u.calls, "status:"+key+":"+text) }
func (u *plainUnsetUI) Input(_ context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.calls = append(u.calls, "input:"+title+":"+placeholder)
	return "", nil
}
func (u *plainUnsetUI) Editor(_ context.Context, title, prefill string) (string, error) {
	u.calls = append(u.calls, "editor:"+title+":"+prefill)
	return "", nil
}

// Pi's setStatus(key, undefined), input(title) and editor(title) leave a member undefined that RPC mode then omits. The runner wraps the bound UI to report dialog
// prompts; the wrapper must still let an in-process extension send the undefined form, through the bound UI's own interfaces or, for a UI without them, the empty
// string.
func TestUIPrompt_UnsetMemberFormsReachTheBoundUI(t *testing.T) {
	bound := &unsetUI{UIContext: extension.NoopUIContext}
	r := inproc.NewRunner(nil, ".")
	r.SetUIContext(bound, extension.ModeRPC)
	ui := runnerUI(t, r)
	clearer, ok := ui.(extension.StatusClearer)
	if !ok {
		t.Fatal("the runner's UI hides the bound UI's StatusClearer")
	}
	placeholder, ok := ui.(extension.PlaceholderOmitter)
	if !ok {
		t.Fatal("the runner's UI hides the bound UI's PlaceholderOmitter")
	}
	prefill, ok := ui.(extension.PrefillOmitter)
	if !ok {
		t.Fatal("the runner's UI hides the bound UI's PrefillOmitter")
	}
	ui.SetStatus("k", "")
	clearer.ClearStatus("k")
	_, _ = placeholder.InputWithoutPlaceholder(t.Context(), "i", extension.ExtensionUIDialogOptions{})
	_, _ = ui.Input(t.Context(), "i", "", extension.ExtensionUIDialogOptions{})
	_, _ = prefill.EditorWithoutPrefill(t.Context(), "e")
	_, _ = ui.Editor(t.Context(), "e", "")
	want := []string{"status:k:", "status:k:<unset>", "input:i:<unset>", "input:i:", "editor:e:<unset>", "editor:e:"}
	if !slices.Equal(bound.calls, want) {
		t.Fatalf("bound UI calls = %q, want %q", bound.calls, want)
	}

	plain := &plainUnsetUI{UIContext: extension.NoopUIContext}
	r.SetUIContext(plain, extension.ModeRPC)
	ui = runnerUI(t, r)
	ui.(extension.StatusClearer).ClearStatus("k")
	_, _ = ui.(extension.PlaceholderOmitter).InputWithoutPlaceholder(t.Context(), "i", extension.ExtensionUIDialogOptions{})
	_, _ = ui.(extension.PrefillOmitter).EditorWithoutPrefill(t.Context(), "e")
	want = []string{"status:k:", "input:i:", "editor:e:"}
	if !slices.Equal(plain.calls, want) {
		t.Fatalf("a UI without the interfaces got %q, want the empty-string fallbacks %q", plain.calls, want)
	}
}
