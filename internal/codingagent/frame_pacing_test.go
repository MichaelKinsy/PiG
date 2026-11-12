package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestAdaptiveFramePacingSelected(t *testing.T) {
	for _, tc := range []struct {
		setting, env string
		want         bool
	}{
		{"adaptive", "", true},
		{"pi", "", false},
		{"", "", false},
		{"faster", "", false},
		{"adaptive", "pi", false},
		{"adaptive", "0", false},
		{"pi", "adaptive", true},
		{"pi", "1", false},
		{"adaptive", "1", true},
		{"adaptive", "PI", true},
	} {
		getenv := func(name string) string {
			if name == framePacingEnv {
				return tc.env
			}
			return ""
		}
		if got := adaptiveFramePacingSelected(tc.setting, getenv); got != tc.want {
			t.Errorf("setting %q, %s=%q: adaptive = %v, want %v", tc.setting, framePacingEnv, tc.env, got, tc.want)
		}
	}
}

// The gate is open while a run is active and closed at the first frame after the run ended, however it ended: its queue drained, it failed, or it was aborted.
func TestFramePacingGateClosesOneFrameAfterTheRunEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(m *InteractiveMode, finish chan<- error)
	}{
		{"the last queued message drained", func(_ *InteractiveMode, finish chan<- error) { finish <- nil }},
		{"error", func(_ *InteractiveMode, finish chan<- error) { finish <- errors.New("provider failed") }},
		{"abort", func(m *InteractiveMode, _ chan<- error) { m.abortFn() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, _ := newLifecycleMode(t)
			m.framePacingInstalled = true
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open before any run")
			}
			started, finish, aborted := make(chan struct{}), make(chan error), m.abortCtx.Done()
			m.runTurn(ctx, "", func(runCtx context.Context) ([]agent.AgentMessage, error) {
				close(started)
				select {
				case err := <-finish:
					return nil, err
				case <-runCtx.Done():
					return nil, runCtx.Err()
				case <-aborted:
					return nil, context.Canceled
				}
			})
			m.queueMu.Lock()
			settled := m.turnSettled
			m.queueMu.Unlock()
			<-started
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate is closed while the run is active")
			}
			tc.end(m, finish)
			<-settled
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open one frame after the run ended")
			}
		})
	}
}

// A compaction closes the gate at the next frame though the run that started it is still active, and its end opens it again.
func TestFramePacingGateIsClosedDuringCompaction(t *testing.T) {
	m, _, _ := newLifecycleMode(t)
	m.framePacingInstalled = true
	m.turnActive.Store(true)
	m.runScheduledRender()
	if !m.framePacingAllowed.Load() {
		t.Fatal("the gate is closed while the run is active")
	}
	m.handleAgentEvent(agent.CompactionStartEvent{})
	if !m.isCompacting {
		t.Fatal("compaction_start did not mark the mode compacting")
	}
	m.runScheduledRender()
	if m.framePacingAllowed.Load() {
		t.Fatal("the gate is open one frame after compaction started")
	}
	m.isCompacting = false
	m.runScheduledRender()
	if !m.framePacingAllowed.Load() {
		t.Fatal("the gate stayed closed after the compaction")
	}
}

type pacingTestComponent struct{}

func (pacingTestComponent) Render(int) []string { return nil }
func (pacingTestComponent) Invalidate()         {}
func (pacingTestComponent) Dispose()            {}

// Each extension surface alone closes the gate at the next frame of an active run, and so does an open overlay.
func TestFramePacingGateIsClosedWhileAnExtensionDrawsOnScreen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mount func(m *InteractiveMode)
	}{
		{"dialog", func(m *InteractiveMode) { m.extensionDialogs = []*extensionDialog{{}} }},
		{"overlay of an extension", func(m *InteractiveMode) { m.extensionOverlays = []*extensionOverlay{{}} }},
		{"open overlay", func(m *InteractiveMode) { m.tuiInst.ShowOverlay(tui.NewText("overlay"), tui.OverlayOptions{}) }},
		{"header", func(m *InteractiveMode) { m.extHeaderComponent = pacingTestComponent{} }},
		{"footer", func(m *InteractiveMode) { m.extFooterComponent = pacingTestComponent{} }},
		{"editor", func(m *InteractiveMode) {
			m.editorFactory = func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent { return nil }
		}},
		{"widget of the host process", func(m *InteractiveMode) { m.inprocessWidgets = []inprocessWidget{{}} }},
		{"widget of an extension process", func(m *InteractiveMode) { m.bridgeWidgets = map[string]*subprocess.PushProxy{"w": nil} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newLifecycleMode(t)
			m.framePacingInstalled = true
			m.turnActive.Store(true)
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate is closed while the run is active and nothing of an extension is mounted")
			}
			tc.mount(m)
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open one frame after the mount")
			}
		})
	}
}

// Without the policy the gate is never written: a run with Pi's interval carries nothing of the policy.
func TestFramePacingGateIsUntouchedWithoutThePolicy(t *testing.T) {
	m, _, _ := newLifecycleMode(t)
	m.turnActive.Store(true)
	m.runScheduledRender()
	if m.framePacingAllowed.Load() {
		t.Fatal("the gate was opened though no policy is installed")
	}
}

// The policy is installed at start unless the run selects Pi's interval.
func TestInstallFramePacingFollowsTheSelection(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{{"pi", false}, {"0", false}, {"adaptive", true}, {"", true}} {
		t.Setenv(framePacingEnv, tc.env)
		m, _, _ := newLifecycleMode(t)
		m.installFramePacing()
		if m.framePacingInstalled != tc.want {
			t.Errorf("%s=%q: policy installed = %v, want %v", framePacingEnv, tc.env, m.framePacingInstalled, tc.want)
		}
	}
}
