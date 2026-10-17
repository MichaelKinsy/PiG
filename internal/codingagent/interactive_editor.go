package codingagent

import (
	"context"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) openExternalEditor(ctx context.Context) {
	m.openExternalEditorBuffer(ctx, m.externalEditorCommand(), m.editor.GetExpandedText(), m.editor.SetText)
}

// openExternalEditorBuffer hands terminal input and output to the child off the UI loop. Completion returns to the owner loop and only a successful edit replaces the caller's buffer.
func (m *InteractiveMode) openExternalEditorBuffer(ctx context.Context, command, initial string, apply func(string)) {
	if m.externalEditorActive {
		return
	}
	m.externalEditorActive = true
	if m.surface != nil {
		// pig additive (D91): the frontend session keeps its frames while
		// the editor owns the terminal and learns of the handoff.
		m.surface.Suspend()
	} else {
		m.tuiInst.Stop()
	}
	if m.inputReader != nil {
		m.inputReader.pause()
	}
	if m.rawRestore != nil {
		m.rawRestore()
		m.rawRestore = nil
		m.rawDrain = nil
	}
	ownerCtx := m.runCtx
	if ownerCtx == nil {
		ownerCtx = ctx
	}
	m.backgroundTasks.Go(func() {
		result, runErr := OpenExternalEditor(ctx, initial, command)
		m.runOnMain(ownerCtx, func() {
			m.externalEditorActive = false
			if ownerCtx.Err() != nil {
				return
			}
			restore, drain, rawErr := tui.EnterRawModeWithDrain()
			if rawErr != nil {
				m.exitIfDeadTerminal(rawErr)
				m.failInputLoop(rawErr)
				return
			}
			m.rawRestore = restore
			m.rawDrain = drain
			if runErr == nil && ctx.Err() == nil {
				apply(result)
			}
			if m.surface != nil {
				// pig additive (D91): the frame after the handoff carries
				// only what changed, such as the edited text.
				m.surface.Resume()
			} else {
				m.tuiInst.Start()
				m.tuiInst.RepaintAll()
			}
			if m.inputReader != nil {
				m.inputReader.resume()
			}
			if resume := m.externalEditorInput; resume != nil {
				m.externalEditorInput = nil
				resume()
			}
		})
	})
}

// toggleAllTools flips the global tool expansion state for the whole
// transcript. Mirrors upstream toggleToolOutputExpansion().
func (m *InteractiveMode) toggleAllTools() {
	m.toolMu.Lock()
	expanded := !m.toolsExpanded
	m.toolMu.Unlock()
	m.setAllToolsExpanded(expanded)
}

// setAllToolsExpanded applies changed expansion state to the startup header and every mounted expandable child, not detached tracking-list entries. Extension UI calls share this owner-loop path with the default editor.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:setToolsExpanded
func (m *InteractiveMode) setAllToolsExpanded(expanded bool) {
	m.toolMu.Lock()
	if m.toolsExpanded == expanded {
		m.toolMu.Unlock()
		return
	}
	m.toolsExpanded = expanded
	m.builtInHeaderExpanded = expanded
	m.toolMu.Unlock()
	for _, container := range []*tui.Container{m.loadedResourcesContainer, m.chatContainer} {
		if container == nil {
			continue
		}
		for _, child := range container.Children() {
			if expandable, ok := child.(interface{ SetExpanded(bool) }); ok {
				expandable.SetExpanded(expanded)
			}
		}
	}
	m.showStatus("Tool output: " + map[bool]string{true: "expanded", false: "collapsed"}[expanded])
}

// rebuildChatFromSession rebuilds the visible conversation from the active
// path-to-leaf messages.

// externalEditorCommand is the settings' externalEditor command (settings-manager.ts getExternalEditorCommand), "" when no settings are loaded.
func (m *InteractiveMode) externalEditorCommand() string {
	if m.opts.SettingsManager == nil {
		return ""
	}
	return m.opts.SettingsManager.GetExternalEditorCommand()
}
