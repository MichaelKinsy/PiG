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
	m.setTerminalTitle(tui.BuildTerminalTitle(name, m.opts.CWD))
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

// bindExtensionModelObject gives the model object of a wire setModel a backend: the Session runtime's (ModelBinder), so the Session's model is the object
// the extension passed. A mode constructed without a binder builds the model the object's provider and id name.
func (m *InteractiveMode) bindExtensionModelObject(object *ai.Model) (*ai.Model, error) {
	if m.opts.ModelBinder != nil {
		return m.opts.ModelBinder(object), nil
	}
	if m.opts.ModelBuilder == nil {
		return nil, fmt.Errorf("model switching not configured")
	}
	return m.opts.ModelBuilder(object.ProviderMeta.ProviderID + "/" + object.ID)
}

// setExtensionModel is the in-process setModel action. Its model object carries its own backend; the wire action's is bound by bindExtensionModelObject.
func (m *InteractiveMode) setExtensionModel(ctx context.Context, model extension.Model) (bool, error) {
	if model == nil {
		return false, errors.New("setModel requires a model")
	}
	m.invalidatePostLoginSelection()
	return m.switchExtensionModel(ctx, model)
}

// switchExtensionModel switches to model for an extension's setModel. A Session answers the action itself: false without configured credentials, and it emits model_select only for a changed model (agent-session.ts:3343-3347, 2372-2384).
// The host action runs off the owner loop. As for /model and cycling, the Session's model and the footer's change in one owner-loop unit, at the moment the Session's model changes: Pi's footer reads state.model on every frame and
// sets it before awaiting model_select (footer.ts:107, 160, 220; agent-session.ts:2644-2663), so a frame drawn while a handler is pending shows the new model. The model_select handlers are awaited here, off the loop.
func (m *InteractiveMode) switchExtensionModel(ctx context.Context, model *ai.Model) (bool, error) {
	handle := m.opts.SessionHandle
	if handle == nil {
		if m.agent != nil {
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
	var notify context.Context
	if err := m.runOnMainAndWait(ctx, func() error { notify = m.modelNotificationContext(ctx); return nil }); err != nil {
		return false, err
	}
	switched, err := handle.ExtensionSetModelOnMain(notify, model, func(mutate func() error) error {
		return m.runOnMainAndWait(ctx, func() error {
			if err := mutate(); err != nil {
				return err
			}
			// Pi's footer reads the current Session's model.
			if m.opts.SessionHandle == handle {
				m.showModel(model)
			}
			return nil
		})
	})
	if err != nil || !switched {
		return false, err
	}
	// A handler may have changed the thinking level, and host actions run off the owner loop, which owns the editor.
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
