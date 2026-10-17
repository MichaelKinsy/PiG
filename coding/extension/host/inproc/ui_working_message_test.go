package inproc_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// workingUI records working-message calls; it implements extension.WorkingMessageClearer as the interactive ExtUIContext does.
type workingUI struct {
	extension.UIContext
	calls []string
}

func (u *workingUI) SetWorkingMessage(message string) { u.calls = append(u.calls, "message:"+message) }
func (u *workingUI) ClearWorkingMessage()             { u.calls = append(u.calls, "default") }
func (u *workingUI) SetHiddenThinkingLabel(label string) {
	u.calls = append(u.calls, "label:"+label)
}
func (u *workingUI) ClearHiddenThinkingLabel() { u.calls = append(u.calls, "label-default") }

// plainWorkingUI forwards SetWorkingMessage only, as a UIContext written before WorkingMessageClearer existed does.
type plainWorkingUI struct {
	extension.UIContext
	calls []string
}

func (u *plainWorkingUI) SetWorkingMessage(message string) {
	u.calls = append(u.calls, "message:"+message)
}
func (u *plainWorkingUI) SetHiddenThinkingLabel(label string) {
	u.calls = append(u.calls, "label:"+label)
}

// Pi's setWorkingMessage() with no argument restores the default label (interactive-mode.ts:2641-2646). The runner wraps the bound UI to report dialog prompts;
// the wrapper must still let an in-process extension restore the default, through the bound UI's own clearer or, for a UI without one, SetWorkingMessage("").
func TestUIPrompt_WorkingMessageClearerReachesTheBoundUI(t *testing.T) {
	bound := &workingUI{UIContext: extension.NoopUIContext}
	r := inproc.NewRunner(nil, ".")
	r.SetUIContext(bound, extension.ModeTUI)
	ui := runnerUI(t, r)
	clearer, ok := ui.(extension.WorkingMessageClearer)
	if !ok {
		t.Fatal("the runner's UI hides the bound UI's WorkingMessageClearer, so an in-process extension cannot restore the default working label")
	}
	ui.SetWorkingMessage("")
	clearer.ClearWorkingMessage()
	if want := []string{"message:", "default"}; !slices.Equal(bound.calls, want) {
		t.Fatalf("bound UI calls = %q, want %q", bound.calls, want)
	}

	plain := &plainWorkingUI{UIContext: extension.NoopUIContext}
	r.SetUIContext(plain, extension.ModeTUI)
	clearer, ok = runnerUI(t, r).(extension.WorkingMessageClearer)
	if !ok {
		t.Fatal("the runner's UI has no WorkingMessageClearer")
	}
	clearer.ClearWorkingMessage()
	if want := []string{"message:"}; !slices.Equal(plain.calls, want) {
		t.Fatalf("a UI without a clearer got %q, want the empty-message fallback %q", plain.calls, want)
	}
}

// The same holds for Pi's setHiddenThinkingLabel() (interactive-mode.ts:2383): the runner's wrapper passes the bound UI's HiddenThinkingLabelClearer through.
func TestUIPrompt_HiddenThinkingLabelClearerReachesTheBoundUI(t *testing.T) {
	bound := &workingUI{UIContext: extension.NoopUIContext}
	r := inproc.NewRunner(nil, ".")
	r.SetUIContext(bound, extension.ModeTUI)
	ui := runnerUI(t, r)
	clearer, ok := ui.(extension.HiddenThinkingLabelClearer)
	if !ok {
		t.Fatal("the runner's UI hides the bound UI's HiddenThinkingLabelClearer, so an in-process extension cannot restore the default label")
	}
	ui.SetHiddenThinkingLabel("")
	clearer.ClearHiddenThinkingLabel()
	if want := []string{"label:", "label-default"}; !slices.Equal(bound.calls, want) {
		t.Fatalf("bound UI calls = %q, want %q", bound.calls, want)
	}

	plain := &plainWorkingUI{UIContext: extension.NoopUIContext}
	r.SetUIContext(plain, extension.ModeTUI)
	clearer, ok = runnerUI(t, r).(extension.HiddenThinkingLabelClearer)
	if !ok {
		t.Fatal("the runner's UI has no HiddenThinkingLabelClearer")
	}
	clearer.ClearHiddenThinkingLabel()
	if want := []string{"label:"}; !slices.Equal(plain.calls, want) {
		t.Fatalf("a UI without a clearer got %q, want the empty-label fallback %q", plain.calls, want)
	}
}
