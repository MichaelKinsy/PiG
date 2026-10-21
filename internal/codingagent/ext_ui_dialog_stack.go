package codingagent

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// The extension dialog stack of upstream's InteractiveMode (packages/coding-agent/src/modes/interactive/interactive-mode.ts:
// pushExtensionDialog, removeExtensionDialog, showTopExtensionDialog and the dialog part of resetExtensionUI).
//
// Dialogs in the editor slot (select, confirm, input, editor and an editor-slot custom component) form a stack on the owner
// loop: a new dialog hides the visible one, and closing a dialog shows the previous one again. Every dialog stays answerable,
// so no dialog call is left pending, and a dialog opened from inside another dialog works.

// pushExtensionDialog shows dialog in the editor slot above the dialogs already open.
func (m *InteractiveMode) pushExtensionDialog(dialog *extensionDialog) {
	m.disposeActiveSelector()
	m.extensionDialogs = append(m.extensionDialogs, dialog)
	m.showTopExtensionDialog()
}

// awaitedDialog is the wait of one owner-loop caller on a dialog.
type awaitedDialog struct {
	dismiss context.CancelFunc
}

// awaitOwnerDialog is Pi's `await this.showExtensionSelector(...)`, showExtensionConfirm or showExtensionEditor for a caller
// that runs on the owner loop: the /tree summary prompts, the import confirmation and the missing-cwd confirmation
// (interactive-mode.ts:5565, 5580, 6547 and promptForMissingSessionCwd). Pi's callers are async, so the dialog joins the
// stack like an extension's and the event loop keeps dispatching to whichever dialog the stack shows. The Go caller waits
// through awaitExtensionUI, which keeps that same loop running while ask opens the dialog on a worker.
//
// ok is false when the dialog was dismissed (Esc, or a timeout) or the TUI is unavailable. A Session replacement or /reload
// disposes the dialog too (resetExtensionUI); Pi leaves the awaiting promise pending then and its continuation never runs,
// but the owner loop cannot park for good in a nested loop that ignores submissions, so the wait ends as a dismissal.
func awaitOwnerDialog[T any](m *InteractiveMode, ctx context.Context, ask func(context.Context, *ExtUIContext) (T, error)) (T, bool) {
	var value T
	dialogCtx, dismiss := context.WithCancel(ctx)
	defer dismiss()
	wait := &awaitedDialog{dismiss: dismiss}
	if m.awaitedDialogs == nil {
		m.awaitedDialogs = map[*awaitedDialog]struct{}{}
	}
	m.awaitedDialogs[wait] = struct{}{}
	defer delete(m.awaitedDialogs, wait)
	err := m.awaitExtensionUI(ctx, func(workCtx context.Context) error {
		stop := context.AfterFunc(workCtx, dismiss)
		defer stop()
		var err error
		value, err = ask(dialogCtx, &ExtUIContext{m: m})
		return err
	})
	return value, err == nil
}

// askSelector is `await showExtensionSelector(title, options)`: the chosen option, or ok false when the selector was dismissed.
func (m *InteractiveMode) askSelector(ctx context.Context, title string, options []string) (string, bool) {
	return awaitOwnerDialog(m, ctx, func(ctx context.Context, ui *ExtUIContext) (string, error) {
		return ui.Select(ctx, title, options, extension.ExtensionUIDialogOptions{})
	})
}

// askConfirm is `await showExtensionConfirm(title, message)`: whether Yes was chosen. Dismissing it answers No.
func (m *InteractiveMode) askConfirm(ctx context.Context, title, message string) bool {
	confirmed, ok := awaitOwnerDialog(m, ctx, func(ctx context.Context, ui *ExtUIContext) (bool, error) {
		return ui.Confirm(ctx, title, message, extension.ExtensionUIDialogOptions{})
	})
	return ok && confirmed
}

// askEditor is `await showExtensionEditor(title, prefill)`: the text, or ok false when the editor was dismissed.
func (m *InteractiveMode) askEditor(ctx context.Context, title, prefill string) (string, bool) {
	return awaitOwnerDialog(m, ctx, func(ctx context.Context, ui *ExtUIContext) (string, error) {
		return ui.Editor(ctx, title, prefill)
	})
}

// removeExtensionDialog removes dialog from the stack. When it was the visible one, the previous dialog or the editor shows.
func (m *InteractiveMode) removeExtensionDialog(dialog *extensionDialog) {
	index := slices.Index(m.extensionDialogs, dialog)
	if index < 0 {
		return
	}
	m.extensionDialogs = slices.Delete(m.extensionDialogs, index, index+1)
	if index == len(m.extensionDialogs) {
		m.showTopExtensionDialog()
	}
}

// finishExtensionDialog stops dialog's countdown and removes it from the stack, as Pi's selector and input finish does.
func (m *InteractiveMode) finishExtensionDialog(dialog *extensionDialog) {
	if dialog.dispose != nil {
		dialog.dispose()
	}
	m.removeExtensionDialog(dialog)
}

// showTopExtensionDialog puts the visible dialog, or the editor when none is open, in the editor slot with focus, and reports
// the dialog's blocked status. A custom overlay whose focus a dialog took gets it back when the dialog goes (tui.ts:616-627
// resumes the overlay once the dialog that blocked its restore is unmounted).
func (m *InteractiveMode) showTopExtensionDialog() {
	var component tui.Component = m.editor
	var blocked *BlockedStatus
	top := m.topExtensionDialog()
	if top != nil {
		component, blocked = top.component, top.blocked
	}
	m.editorContainer.SetChildren(component)
	m.tuiInst.SetFocus(component)
	m.programStatusReporter().SetBlocked(extensionDialogStatusSource, blocked)
	if shown := m.shownExtensionDialog(); shown != nil {
		m.setExtensionDialogViewMode(true, m.extensionDialogLines(shown.component))
	} else {
		m.setExtensionDialogViewMode(false, 0)
	}
	m.tuiInst.RequestRender()
}

// topExtensionDialog is the visible entry of the stack, nil when the editor shows.
func (m *InteractiveMode) topExtensionDialog() *extensionDialog {
	if len(m.extensionDialogs) == 0 {
		return nil
	}
	return m.extensionDialogs[len(m.extensionDialogs)-1]
}

// shownExtensionDialog is the visible select, confirm, input or editor dialog, which takes the terminal's keys. It is nil
// when the editor, or a custom component that reads its keys through focus, shows.
func (m *InteractiveMode) shownExtensionDialog() *extensionDialog {
	if top := m.topExtensionDialog(); top != nil && top.handle != nil {
		return top
	}
	return nil
}

// resetExtensionDialogs disposes every open dialog and shows the editor. The calls behind the dialogs end with their own
// contexts, and an owner-loop caller waiting on one (awaitOwnerDialog) stops waiting.
func (m *InteractiveMode) resetExtensionDialogs() {
	if len(m.extensionDialogs) == 0 {
		return
	}
	for _, dialog := range m.extensionDialogs {
		if dialog.dispose != nil {
			dialog.dispose()
		}
	}
	m.extensionDialogs = nil
	for wait := range m.awaitedDialogs {
		wait.dismiss()
	}
	m.showTopExtensionDialog()
}
