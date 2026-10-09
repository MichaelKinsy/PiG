package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// holdThenAnswerProvider blocks its first request until the request context is cancelled and answers every later one.
type holdThenAnswerProvider struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

func (p *holdThenAnswerProvider) ID() string   { return "faux" }
func (p *holdThenAnswerProvider) Close() error { return nil }

func (p *holdThenAnswerProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first {
		close(p.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	return stream, nil
}

// Pi 1.1.0 agent-session.ts:1080-1085: interactive mode's agent_settled extension event carries aborted=true for a run stopped with
// Escape (abort() sets _agentRunAbortRequested while the run is active) and aborted=false for the next, finished run.
func TestInteractiveAgentSettledReportsEscapeAbort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	settled := make(chan bool, 2)
	ext := extension.Extension{Name: "settled", Handlers: map[string][]extension.HandlerFn{"agent_settled": {func(args ...any) (any, error) {
		settled <- args[0].(extension.AgentSettledEvent).Aborted
		return nil, nil
	}}}}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
	provider := &holdThenAnswerProvider{started: make(chan struct{})}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
	}, nil)
	next := func(want bool) {
		t.Helper()
		select {
		case got := <-settled:
			if got != want {
				t.Fatalf("agent_settled aborted = %v, want %v", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("agent_settled did not fire")
		}
	}
	h.Do(func() { h.Enter("hold") })
	select {
	case <-provider.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the provider request did not start")
	}
	h.Do(func() { h.Key("\x1b") })
	next(true)
	h.WaitIdle(t, 10*time.Second)
	h.Do(func() { h.Enter("again") })
	next(false)
}

// Pi 1.1.0 program-status-reporter.ts:66-68 sets the resting status on agent_settled: idle for a run stopped with Escape,
// otherwise the run's outcome. Interactive mode drives its own agent_settled (interactive_turn.go), so the reporter must
// hear it there: without it every run stays working after it ends, on an OSC 7501 terminal and in a frontend session.
func TestInteractiveRunSettlesTheProgramStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(nil, cwd)
	provider := &holdThenAnswerProvider{started: make(chan struct{})}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
	}, nil)
	var states func() []tui.ProgramState
	h.Do(func() { states = h.ObserveProgramStatus() })
	// settle waits until the reporter's latest state is want and returns every state it reported.
	settle := func(want tui.ProgramState) []tui.ProgramState {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var got []tui.ProgramState
			h.Do(func() { got = states() })
			if len(got) > 0 && got[len(got)-1] == want {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatalf("reported states %v, want them to end with %s", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	h.Do(func() { h.Enter("hold") })
	select {
	case <-provider.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the provider request did not start")
	}
	h.Do(func() { h.Key("\x1b") })
	settle(tui.ProgramStateIdle)
	h.WaitIdle(t, 10*time.Second)
	h.Do(func() { h.Enter("again") })
	got := settle(tui.ProgramStateDone)
	if want := []tui.ProgramState{tui.ProgramStateWorking, tui.ProgramStateIdle, tui.ProgramStateWorking, tui.ProgramStateDone}; !slices.Equal(got, want) {
		t.Fatalf("reported states %v, want %v", got, want)
	}
}
