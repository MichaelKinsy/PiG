package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// RebindSession installs a factory result on the test owner and drives the production rebind path.
func (h *TestHarness) RebindSession(ctx context.Context, session InteractiveSessionHandle, runner *inproc.Runner, event *extension.SessionStartEvent, before bool) error {
	h.Do(func() {
		h.m.opts.SessionHandle = session
		h.m.opts.CWD = session.Inner().GetCwd()
		h.m.newRunner = runner
		h.m.opts.ExtensionRunner = runner
		h.m.opts.SessionStartEvent = event
	})
	return h.m.rebindCurrentSession(ctx, before)
}

// SetRebindResources supplies the actual factory-created settings and model registry on the owner.
func (h *TestHarness) SetRebindResources(settings *SettingsManager, registry *ModelRegistry) {
	h.m.opts.SettingsManager = settings
	h.m.opts.Settings = settings.Get()
	h.m.opts.ModelRegistry = registry
}

// RebindOwnsSession reports whether the mode applied the factory result before callbacks. Call it through Do.
func (h *TestHarness) RebindOwnsSession() bool {
	return h.m.agent == h.m.opts.SessionHandle.Agent()
}

// RebindSubscribed reports the actual channel selected by the owner. Call it through Do.
func (h *TestHarness) RebindSubscribed() bool {
	return h.m.eventCh != nil && h.m.eventCh == h.m.opts.SessionHandle.Events()
}

// RebindSubscribedTo observes the selected channel without invoking the Session's Events getter. Call it through Do.
func (h *TestHarness) RebindSubscribedTo(events <-chan agent.AgentEvent) bool {
	return h.m.eventCh != nil && h.m.eventCh == events
}

// ObserveRebindTitles intercepts the existing terminal-title sink without changing the rebind implementation.
func ObserveRebindTitles(t testing.TB, notify func(string)) {
	t.Helper()
	previous := setTerminalTitle
	setTerminalTitle = notify
	t.Cleanup(func() { setTerminalTitle = previous })
}

// LoadStartupResources runs the resource loads Run performs before it binds the Session: prompt templates and theme paths.
func (h *TestHarness) LoadStartupResources() {
	h.Do(func() {
		h.m.loadPromptTemplates()
		h.m.loadThemes()
	})
}
