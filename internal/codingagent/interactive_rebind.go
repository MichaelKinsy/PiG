package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// runOnMainAndWait waits for an accepted owner operation, including its awaited work. A cancelled queued operation does not mutate the UI, and a caller whose ctx ends before the loop starts the operation abandons it.
func (m *InteractiveMode) runOnMainAndWait(ctx context.Context, work func() error) error {
	if m.onOwnerLoop() {
		// Queueing and waiting from the owner loop would wait for work only this goroutine can run.
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		return work()
	}
	result := make(chan error, 1)
	// claimed decides once whether the loop runs the operation or the cancelled caller abandons it.
	var claimed atomic.Bool
	if err := m.postToMain(ctx, func() {
		if !claimed.CompareAndSwap(false, true) {
			return
		}
		if ctx.Err() != nil {
			result <- context.Cause(ctx)
			return
		}
		result <- work()
	}); err != nil {
		return err
	}
	var stopped <-chan struct{}
	if m.runCtx != nil {
		stopped = m.runCtx.Done()
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		if claimed.CompareAndSwap(false, true) {
			return context.Cause(ctx)
		}
		return <-result
	case <-stopped:
		return context.Cause(m.runCtx)
	case <-m.runEnded.endedCh():
		// The loop ended with the task queued or running: a task it never started is abandoned, one it started has its result.
		if claimed.CompareAndSwap(false, true) {
			return fmt.Errorf("%w: the owner loop ended", errOwnerLoopUnavailable)
		}
		return <-result
	}
}

// rebindCurrentSession captures the Session before awaiting extension binding. A replaced startup bind cannot subscribe or update the replacement's title on completion.
func (m *InteractiveMode) rebindCurrentSession(ctx context.Context, renderBeforeBind bool) error {
	return m.rebindSession(ctx, renderBeforeBind, func(ctx context.Context, _ *inproc.Runner, emit func(context.Context) error) error { return emit(ctx) })
}

// bindStartupSession is Init's `await this.rebindCurrentSession()` (interactive-mode.ts init(), after setupEditorSubmitHandler at :1127-1128 and before
// run() shows the startup notices at :1257). Node reads stdin only between tasks, so an await whose promise settles in microtasks lets no key a user typed
// ahead reach the editor: a bind with no handler to wait for (agent-session.ts bindExtensions emits session_start, then resources_discover when one is
// registered) runs to the end of init() and the notices before any key is read. Pig does the same: with no handler to run the bind is plain code on Init's
// goroutine, and only a bind that runs handlers serves terminal input while they run, as Pi's event loop does while they pend. The owner steps before and
// after the emit run on Init's own goroutine in both cases, so a queued key cannot be read between the handlers' end and the notices either.
func (m *InteractiveMode) bindStartupSession(ctx context.Context) error {
	defer m.enterOwnerLoop()()
	return m.rebindSession(ctx, false, func(ctx context.Context, runner *inproc.Runner, emit func(context.Context) error) error {
		if !bindMayPend(runner) {
			return emit(ctx)
		}
		return m.awaitExtensionUI(ctx, emit)
	})
}

// bindMayPend reports whether binding the runner's extensions runs a handler: session_start handlers and, when one is registered, resources_discover
// handlers (agent-session.ts:3433-3436, 3439-3442). Nothing else of the bind waits.
func bindMayPend(runner *inproc.Runner) bool {
	return runner != nil && (runner.HasHandlers(EventSessionStart) || runner.HasHandlers(EventResourcesDiscover) && !runner.IsStale())
}

// rebindSession runs the steps of a rebind: the owner steps before and after the emit through runOnMainAndWait, and the emit of session_start and
// resources_discover through awaitEmit, which decides whether the owner loop serves input while the handlers run.
func (m *InteractiveMode) rebindSession(ctx context.Context, renderBeforeBind bool, awaitEmit func(ctx context.Context, runner *inproc.Runner, emit func(context.Context) error) error) error {
	var session InteractiveSessionHandle
	var runner *inproc.Runner
	var cwd string
	var event extension.SessionStartEvent
	if err := m.runOnMainAndWait(ctx, func() error {
		session, runner, cwd = m.opts.SessionHandle, m.newRunner, m.opts.CWD
		if session == nil {
			return errors.New("interactive: SessionHandle is required")
		}
		// The events the old Session delivered are applied before its channel is dropped, as they were when they arrived.
		m.setEventRegion(false)
		m.eventCh = nil
		m.unsubscribeSessionListener()
		m.programStatusReporter().Reset()
		m.applyRuntimeSettings()
		// interactive-mode.ts applyRuntimeSettings: this.footer.setSession(this.session)
		m.statusLine.SetSession(m)
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
	if err := awaitEmit(ctx, runner, func(ctx context.Context) error {
		if runner == nil {
			return nil
		}
		if _, err := runner.Emit(ctx, event); err != nil {
			return err
		}
		// upstream: agent-session.ts:3207 (bindExtensions)
		runner.ReportUnhandledMcpServers()
		if runner.HasHandlers(EventResourcesDiscover) && !runner.IsStale() {
			var err error
			resources, err = runner.EmitResourcesDiscover(ctx, cwd, ResourcesDiscoverReason(event.Reason))
			return err
		}
		return nil
	}); err != nil {
		return err
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

// runSignalNotifier is what the extension host's UI bridge offers the owner of the agent's run: a report that a run began or ended.
type runSignalNotifier interface{ RunSignalChanged() }

// setAgent makes next the Session's agent loop. The main loop reads m.agent; the extension host reads ctx.signal from its own goroutines through extensionAgent, which Session replacement republishes here. Run changes of next are reported to the extension host's UI bridge, as Pi's ctx.signal getter (`() => this.agent.signal`, agent-session.ts:3368) is live at every read, and the retired agent's are no longer reported.
func (m *InteractiveMode) setAgent(next *agent.Agent) {
	m.agent = next
	m.extensionAgent.Store(next)
	if m.stopRunSignalReports != nil {
		m.stopRunSignalReports()
		m.stopRunSignalReports = nil
	}
	if notifier, ok := m.opts.SubprocessUIBridge.(runSignalNotifier); ok && next != nil {
		m.stopRunSignalReports = next.ObserveRunSignal(notifier.RunSignalChanged)
	}
}

func (m *InteractiveMode) subscribeToAgent() {
	m.setEventRegion(false)
	m.eventCh = m.opts.SessionHandle.Events()
	m.subscribeSessionListener()
}

func (m *InteractiveMode) applyRuntimeSettings() {
	m.setAgent(m.opts.SessionHandle.Agent())
	m.installRunPromptTurnRefresh()
	m.opts.Model = m.agent.Model()
	m.hideThinking = m.opts.Settings.GetHideThinkingBlock()
	m.outputPad = int(m.opts.Settings.GetOutputPad())
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

// renderCurrentSessionState clears transient state and redraws the current Session, including its footer name
// (interactive-mode.ts renderCurrentSessionState). The transcript repaint also forgets the pending tool cards.
func (m *InteractiveMode) renderCurrentSessionState() {
	if m.loadedResourcesContainer != nil {
		m.loadedResourcesContainer.Clear()
	}
	if m.pendingMessagesContainer != nil {
		m.pendingMessagesContainer.Clear()
	}
	m.pendingBashBlocksMu.Lock()
	m.pendingBashBlocks = nil
	m.pendingBashBlocksMu.Unlock()
	m.compactionQueue = nil
	m.evCurrentBlock = nil
	m.repaintInitialMessages()
}
