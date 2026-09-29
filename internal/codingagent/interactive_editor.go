package codingagent

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) openExternalEditor(ctx context.Context) {
	m.openExternalEditorBuffer(ctx, m.editor.GetExpandedText(), m.editor.SetText)
}

// openExternalEditorBuffer hands terminal input and output to the child off the UI loop. Completion returns to the owner loop and only a successful edit replaces the caller's buffer.
func (m *InteractiveMode) openExternalEditorBuffer(ctx context.Context, initial string, apply func(string)) {
	if m.externalEditorActive {
		return
	}
	m.externalEditorActive = true
	if m.themeState.autoSyncEnabled.Load() {
		m.writeThemeNotifications(false)
	}
	m.tuiInst.Stop()
	if m.inputReader != nil {
		m.inputReader.pause()
	}
	if m.rawRestore != nil {
		m.rawRestore()
		m.rawRestore = nil
		m.rawDrain = nil
	}
	command := ""
	if m.opts.SettingsManager != nil {
		command = m.opts.SettingsManager.GetExternalEditorCommand()
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
				m.failInputLoop(rawErr)
				return
			}
			m.rawRestore = restore
			m.rawDrain = drain
			if runErr == nil && ctx.Err() == nil {
				apply(result)
			}
			m.tuiInst.Start()
			if m.themeState.autoSyncEnabled.Load() {
				m.writeThemeNotifications(true)
			}
			m.tuiInst.RepaintAll()
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

// setGenericToolArgs retains arguments only for extension tools that use the
// generic call renderer. Built-ins and tools with a custom call renderer keep
// their existing presentation contract.
func (m *InteractiveMode) setGenericToolArgs(comp *tui.ToolExecutionComponent, name string, args json.RawMessage) {
	if comp == nil || m.newRunner == nil {
		return
	}
	definition, ok := m.newRunner.GetToolDefinition(name)
	// An override of a built-in name draws the built-in renderers it does not
	// define (upstream renderers/index.ts withBuiltInRenderers).
	if !ok || definition.RenderCall != nil || tui.HasBuiltInToolRenderers(name) {
		return
	}
	// pig divergence (D59): generic extension cards retain complete args so
	// every collapsed omission is recoverable through the global details toggle.
	comp.SetStructuredArgs(args)
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
	if !expanded && m.tuiInst != nil {
		// Collapsing can remove more rows than the viewport contains. Those
		// expanded rows already live in native scrollback and differential
		// repainting cannot erase them, so rebuild the transcript in its
		// collapsed form. This clear is tied to the user's explicit action;
		// automatic tool completion follows the ordinary Pi redraw path.
		m.tuiInst.ForceFullRender()
	}
}

// rebuildChatFromSession rebuilds the visible conversation from the active
// path-to-leaf messages.
