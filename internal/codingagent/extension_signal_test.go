package codingagent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi binds ctx.signal to `this.agent.signal` (agent-session.ts:3368): the signal of the run in progress, undefined when no run is active (agent.ts:336-338).
// Interactive mode hands extensions that same signal from its agent: it is live during a run, cancelled by the run's abort, and gone once the run ends.
func TestExtensionSignalIsTheAgentRunSignal(t *testing.T) {
	started := make(chan context.Context, 1)
	m := &InteractiveMode{}
	// The agent is installed as a Session binding installs it: extensionSignal reads the extension-facing reference setAgent publishes.
	m.setAgent(agent.NewAgent(agent.AgentOptions{
		Model: &ai.Model{ID: "blocking"},
		StreamFn: func(ctx context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			started <- ctx
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}))
	if m.extensionSignal() != nil {
		t.Fatal("ctx.signal is set while no run is active")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.agent.Send(t.Context(), "work")
	}()
	<-started
	signal := m.extensionSignal()
	if signal == nil || signal.Err() != nil {
		t.Fatalf("ctx.signal = %v during a run, want a live signal", signal)
	}
	if again := m.extensionSignal(); again != signal {
		t.Fatal("two reads during one run returned different signals")
	}
	m.agent.Abort()
	if signal.Err() == nil {
		t.Fatal("aborting the run did not cancel ctx.signal")
	}
	<-done
	if m.extensionSignal() != nil {
		t.Fatal("ctx.signal is still set after the run ended")
	}
}

// runSignalBridge records the run-signal changes the interactive owner reports to the extension host.
type runSignalBridge struct {
	SubprocessUIBridge
	mu     sync.Mutex
	states []bool
	signal func() context.Context
}

func (b *runSignalBridge) RunSignalChanged() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.states = append(b.states, b.signal() != nil)
}

func (b *runSignalBridge) changes() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.states)
}

func quickAgentStream(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return nil, errors.New("provider unavailable")
}

func quickAgent() *agent.Agent {
	return agent.NewAgent(agent.AgentOptions{Model: &ai.Model{ID: "quick"}, StreamFn: quickAgentStream})
}

// Pi reads `this.agent.signal` when the extension asks (agent-session.ts:3368, agent.ts:336-338), so every extension runtime sees a run begin and end the moment it does.
// Interactive mode reports each change of its current agent's run to the extension host, and stops reporting an agent a replacement Session retired.
func TestInteractiveModeReportsRunSignalChangesOfItsCurrentAgent(t *testing.T) {
	m := &InteractiveMode{}
	bridge := &runSignalBridge{signal: m.extensionSignal}
	m.opts.SubprocessUIBridge = bridge
	first, second := quickAgent(), quickAgent()
	m.setAgent(first)
	_, _ = first.Send(t.Context(), "work")
	if want := []bool{true, false}; !slices.Equal(bridge.changes(), want) {
		t.Fatalf("signal states reported for the first agent's run = %v, want %v", bridge.changes(), want)
	}
	m.setAgent(second)
	_, _ = first.Send(t.Context(), "work") // the replaced Session's agent no longer reports.
	_, _ = second.Send(t.Context(), "work")
	if want := []bool{true, false, true, false}; !slices.Equal(bridge.changes(), want) {
		t.Fatalf("signal states reported = %v, want %v", bridge.changes(), want)
	}
}

// The extension host reads ctx.signal from its own goroutines while a Session replacement reassigns the agent on the main loop (interactive_rebind.go applyRuntimeSettings), so the agent is shared state: run with -race.
func TestInteractiveModeExtensionSignalReadsAnAgentReplacedConcurrently(t *testing.T) {
	m := &InteractiveMode{}
	m.setAgent(quickAgent())
	replacement := quickAgent()
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.extensionSignal()
				}
			}
		})
	}
	// Replace the agent for as long as the readers need to overlap with it.
	for deadline := time.Now().Add(100 * time.Millisecond); time.Now().Before(deadline); {
		m.setAgent(replacement)
		m.setAgent(nil)
	}
	close(stop)
	readers.Wait()
}

// The production bridge is what interactive mode reports run changes to.
var _ runSignalNotifier = (*subprocess.UIBridge)(nil)

// A Session replacement runs applyRuntimeSettings on the main loop (interactive_rebind.go): it must publish the replacement's agent to the extension host and report that agent's runs to the bridge the replacement installed.
func TestApplyRuntimeSettingsPublishesTheReplacementAgentToTheExtensionHost(t *testing.T) {
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	bridge := &runSignalBridge{signal: m.extensionSignal}
	m.opts.SubprocessUIBridge = bridge
	replacement := agent.NewAgent(agent.AgentOptions{Model: model, StreamFn: quickAgentStream})
	m.opts.SessionHandle = &replacementRecordingHandle{recordingCompactHandle: &recordingCompactHandle{agent: replacement}}
	m.applyRuntimeSettings()
	_, _ = replacement.Send(t.Context(), "work")
	if want := []bool{true, false}; !slices.Equal(bridge.changes(), want) {
		t.Fatalf("signal states reported for the replacement's run = %v, want %v", bridge.changes(), want)
	}
}
