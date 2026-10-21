package codingagent

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// These are the Session-backed ExtensionActions interactive mode binds for the extensions of its in-process runner and, through the host actions of the same names, for subprocess extensions. Pi binds all of them in every mode (agent-session.ts:3416-3465 _bindExtensionCore); the mode's bind replaces the Session's, so the mode supplies each one.

// extensionSessionName is the getSessionName action.
func (m *InteractiveMode) extensionSessionName() string {
	if session := m.currentSession(); session != nil {
		return session.GetSessionName()
	}
	return ""
}

// setExtensionSessionName is the setSessionName action: it persists the name, then updates the footer and the terminal title as the /name command does.
func (m *InteractiveMode) setExtensionSessionName(name string) error {
	if m.currentSession() == nil {
		return fmt.Errorf("session not available")
	}
	if m.opts.SessionHandle != nil {
		if err := m.opts.SessionHandle.SetSessionName(name); err != nil {
			return err
		}
	} else {
		_, current, err := m.currentSession().AppendSessionInfoName(name)
		if err != nil {
			return err
		}
		emitSessionInfoChanged(m.newRunner, current)
	}
	name = m.currentSession().GetSessionName()
	if m.statusLine != nil {
		m.statusLine.SetName(name)
	}
	tui.SetTerminalTitle(tui.BuildTerminalTitle(name, m.opts.CWD))
	return nil
}

// extensionThinkingLevel is the getThinkingLevel action.
func (m *InteractiveMode) extensionThinkingLevel() extension.ThinkingLevel {
	if m.agent == nil {
		return ""
	}
	return m.agent.ThinkingLevel()
}

// setExtensionThinkingLevel is the setThinkingLevel action. It runs off the owner loop, so the editor and footer refresh is posted to it.
func (m *InteractiveMode) setExtensionThinkingLevel(level ai.ModelThinkingLevel) {
	if m.opts.SessionHandle != nil {
		if err := m.opts.SessionHandle.SetThinkingLevel(level); err != nil {
			m.runOnMain(m.runCtx, func() { m.showError(err.Error()) })
			return
		}
	} else {
		m.agent.SetThinkingLevel(ai.ClampThinkingLevel(m.agent.Model(), level))
	}
	m.runOnMain(m.runCtx, m.refreshThinkingLevel)
}

// setExtensionModel is the in-process setModel action. It takes the model object where the wire action takes a spec.
func (m *InteractiveMode) setExtensionModel(ctx context.Context, model extension.Model) (bool, error) {
	if model == nil {
		return false, errors.New("setModel requires a model")
	}
	m.invalidatePostLoginSelection()
	return m.switchExtensionModel(ctx, model)
}

// switchExtensionModel switches to model for an extension's setModel. A Session answers the action itself: false without configured credentials, and it emits model_select only for a changed model (agent-session.ts:3343-3347, 2372-2384).
func (m *InteractiveMode) switchExtensionModel(ctx context.Context, model *ai.Model) (bool, error) {
	if m.opts.SessionHandle != nil {
		switched, err := m.opts.SessionHandle.ExtensionSetModel(ctx, model)
		if err != nil || !switched {
			return false, err
		}
	} else if m.agent != nil {
		m.agent.SetModel(model)
	}
	m.opts.Model = model
	if m.statusLine != nil {
		m.statusLine.SetModel(model)
	}
	// Host actions run off the owner loop, which owns the editor.
	m.runOnMain(m.runCtx, m.refreshThinkingLevel)
	return true, nil
}

// appendExtensionEntry is the appendEntry action: it writes the custom entry to the session log and renders it into the transcript, as the wire action does for pi.appendEntry.
func (m *InteractiveMode) appendExtensionEntry(customType string, data any) error {
	session := m.currentSession()
	if session == nil {
		return fmt.Errorf("session not available")
	}
	entry, err := AppendExtensionEntry(session, customType, data, nil)
	if err != nil {
		return err
	}
	// The action runs off the owner loop, which owns the transcript.
	m.runOnMain(m.runCtx, func() {
		m.addCustomEntryToChat(entry)
		m.tuiInst.Render()
	})
	return nil
}
