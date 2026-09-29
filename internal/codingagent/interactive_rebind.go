package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// runOnMainAndWait waits for an accepted owner operation, including its awaited work. A cancelled queued operation does not mutate the UI.
func (m *InteractiveMode) runOnMainAndWait(ctx context.Context, work func() error) error {
	result := make(chan error, 1)
	if err := m.postToMain(ctx, func() {
		if ctx.Err() != nil {
			result <- context.Cause(ctx)
			return
		}
		result <- work()
	}); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-m.runCtx.Done():
		return context.Cause(m.runCtx)
	}
}

// rebindCurrentSession captures the Session before awaiting extension binding. A replaced startup bind cannot subscribe or update the replacement's title on completion.
func (m *InteractiveMode) rebindCurrentSession(ctx context.Context, renderBeforeBind bool) error {
	var session InteractiveSessionHandle
	var runner *inproc.Runner
	var cwd string
	var event extension.SessionStartEvent
	if err := m.runOnMainAndWait(ctx, func() error {
		session, runner, cwd = m.opts.SessionHandle, m.newRunner, m.opts.CWD
		if session == nil {
			return errors.New("interactive: SessionHandle is required")
		}
		m.eventCh = nil
		m.applyRuntimeSettings()
		if renderBeforeBind {
			m.renderCurrentSessionState()
			m.subscribeToAgent()
		}
		m.wireInprocContextActions()
		event = extension.SessionStartEvent{Type: EventSessionStart, Reason: "startup"}
		if m.opts.SessionStartEvent != nil {
			event = *m.opts.SessionStartEvent
		}
		return nil
	}); err != nil {
		return err
	}
	var resources *extension.ResourcesDiscoverAggregateResult
	if runner != nil {
		if _, err := runner.Emit(ctx, event); err != nil {
			return err
		}
		if runner.HasHandlers(EventResourcesDiscover) && !runner.IsStale() {
			var err error
			resources, err = runner.EmitResourcesDiscover(ctx, cwd, ResourcesDiscoverReason(event.Reason))
			if err != nil {
				return err
			}
		}
	}
	return m.runOnMainAndWait(ctx, func() error {
		if m.opts.SessionHandle != session {
			return nil
		}
		if err := m.applyDiscoveredResources(resources); err != nil {
			return err
		}
		if !renderBeforeBind {
			m.subscribeToAgent()
		}
		m.setupExtensionShortcutListener(m.runCtx)
		m.editor.SetAutocomplete(m.buildAutocompleteProvider())
		m.updateProviderInfo()
		m.editor.ThinkingLevel = m.thinkingLevel
		m.editor.Invalidate()
		m.updateTerminalTitle()
		m.showLoadedResources(false, true)
		return nil
	})
}

func (m *InteractiveMode) subscribeToAgent() {
	m.eventCh = m.opts.SessionHandle.Events()
}

func (m *InteractiveMode) applyRuntimeSettings() {
	m.agent = m.opts.SessionHandle.Agent()
	m.opts.Model = m.agent.Model()
	m.hideThinking = m.opts.Settings.GetHideThinkingBlock()
	m.outputPad = m.opts.Settings.GetOutputPad()
	m.thinkingLevel = string(m.agent.ThinkingLevel())
	if m.statusLine != nil {
		m.statusLine.timings = m.agent.Timings()
		m.statusLine.SetModel(m.opts.Model)
		m.statusLine.SetName(m.currentSession().GetSessionName())
		m.statusLine.SetCwd(m.opts.CWD)
		m.statusLine.SetThinkingLevel(m.thinkingLevel)
	}
	if m.extCtx != nil {
		m.extCtx.Session = m.currentSession()
	}
}

// renderCurrentSessionState clears transient state and redraws the current Session, including its footer name.
func (m *InteractiveMode) renderCurrentSessionState() {
	if m.loadedResourcesContainer != nil {
		m.loadedResourcesContainer.Clear()
	}
	if m.pendingMessagesContainer != nil {
		m.pendingMessagesContainer.Clear()
	}
	m.compactionQueue = nil
	m.rebuildChatFromSession()
}
