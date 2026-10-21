package testfixture

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// hooksEditor is a CustomEditor subclass that sets the handlers Pi's CustomEditor keeps as public fields (onEscape, onCtrlD, onPasteImage,
// actionHandlers through onAction, onExtensionShortcut), reads and sets the members it inherits from Pi's Editor on its base ("M"), and
// hears the host's working status and invalidate. Each handler writes what ran into the host's text
// (TestEditorComponentAppHandlersRunInTheExtension, TestEditorComponentInheritedMembersReachTheHost,
// TestEditorComponentHearsTheWorkingStatusAndInvalidate).
type hooksEditor struct{ conformanceEditor }

func newHooksEditor(base *sdk.Editor) *hooksEditor {
	say := func(what string) func() error { return func() error { return base.SetText("raw:hook=" + what) } }
	base.SetOnEscape(say("escape"))
	base.SetOnCtrlD(say("ctrl-d"))
	base.SetOnPasteImage(say("paste-image"))
	base.OnAction("app.clear", say("clear"))
	// actionHandlers is the live map: an entry deleted from it is no longer the component's.
	base.OnAction("app.unused", say("unused"))
	delete(base.ActionHandlers(), "app.unused")
	base.SetOnExtensionShortcut(func(data string) (bool, error) {
		if data != "S" {
			return false, nil
		}
		return true, base.SetText("raw:hook=shortcut")
	})
	return &hooksEditor{conformanceEditor{Editor: base}}
}

func (e *hooksEditor) HandleInput(data string) error {
	if data != "M" {
		return e.conformanceEditor.HandleInput(data)
	}
	padding, err := e.GetPaddingX()
	if err != nil {
		return err
	}
	visible, err := e.GetAutocompleteMaxVisible()
	if err != nil {
		return err
	}
	border, err := e.BorderColor("x")
	if err != nil {
		return err
	}
	if err := e.SetDisableSubmit(true); err != nil {
		return err
	}
	disabled, err := e.DisableSubmit()
	if err != nil {
		return err
	}
	if err := e.SetFocused(true); err != nil {
		return err
	}
	focused, err := e.Focused()
	if err != nil {
		return err
	}
	provider := &kit.Autocomplete{Commands: []kit.SlashCommand{{Name: "help", Description: "Show help"}}, BasePath: "/work"}
	if err := e.SetAutocompleteProvider(provider); err != nil {
		return err
	}
	if err := e.OnSubmit("sub"); err != nil {
		return err
	}
	if err := e.OnChange("chg"); err != nil {
		return err
	}
	return e.Editor.SetText(fmt.Sprintf("raw:members=padding=%d,max=%d,border=%s,disable=%t,focused=%t", padding, visible, border, disabled, focused))
}

// SetWorkingStatusIndicator writes the status the host snapshot named.
func (e *hooksEditor) SetWorkingStatusIndicator(status *sdk.EditorStatus) error {
	if status == nil {
		return e.Editor.SetText("raw:status=none")
	}
	return e.Editor.SetText(fmt.Sprintf("raw:status=%s/%s/%d", status.Kind, status.Message, status.Frame))
}

// Invalidate writes that the theme changed.
func (e *hooksEditor) Invalidate() error { return e.Editor.SetText("raw:invalidated") }

func registerEditorHooks(ext *sdk.Extension) {
	ext.Command("editor-install-hooks", "Install a custom editor that sets CustomEditor's handlers", func(ctx sdk.Context, _ string) error {
		return ctx.SetEditorComponent(func(base *sdk.Editor) sdk.EditorComponent { return newHooksEditor(base) })
	})
}
