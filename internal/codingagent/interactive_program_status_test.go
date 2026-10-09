package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// scriptedOutcomeProvider ends each request with the next scripted assistant message.
type scriptedOutcomeProvider struct{ outcomes chan *ai.AssistantMessage }

func (p *scriptedOutcomeProvider) ID() string   { return "faux" }
func (p *scriptedOutcomeProvider) Close() error { return nil }

func (p *scriptedOutcomeProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	var message *ai.AssistantMessage
	select {
	case message = <-p.outcomes:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	message.Timestamp = time.Now().UnixMilli()
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	if message.StopReason == ai.StopReasonAborted {
		// An aborted outcome holds the response until the run is aborted.
		go func() {
			<-ctx.Done()
			_ = stream.Push(ai.ErrorEvent{Reason: message.StopReason, Error: message})
		}()
		return stream, nil
	}
	if message.StopReason == ai.StopReasonError {
		_ = stream.Push(ai.ErrorEvent{Reason: message.StopReason, Error: message})
	} else {
		_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	}
	return stream, nil
}

// Pi 1.1.0 interactive-mode.ts handleEvent forwards the Session's agent_settled to the program status reporter, which then reports the
// run outcome (program-status-reporter.ts:66-68): done after a successful run, error with the first line of the message after a failed
// one, and idle after an aborted one. Pig's interactive mode settles the run itself; it emitted agent_settled to extensions only, so the
// terminal kept reporting working.
func TestInteractiveRunsSettleTheProgramStatus(t *testing.T) {
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
	provider := &scriptedOutcomeProvider{outcomes: make(chan *ai.AssistantMessage, 3)}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(),
	}, nil)
	reports := h.RecordProgramStatus("")

	for _, tc := range []struct {
		name    string
		outcome *ai.AssistantMessage
		want    tui.ProgramStatus
	}{
		{"done", &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop},
			tui.ProgramStatus{App: icodingagent.AppName, State: tui.ProgramStateDone}},
		{"error", &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonError, ErrorMessage: "quota exhausted\nrequest id 42"},
			tui.ProgramStatus{App: icodingagent.AppName, State: tui.ProgramStateError, Message: "quota exhausted"}},
		{"aborted", &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonAborted, ErrorMessage: "This operation was aborted"},
			tui.ProgramStatus{App: icodingagent.AppName, State: tui.ProgramStateIdle}},
	} {
		start := len(reports())
		provider.outcomes <- tc.outcome
		h.Do(func() { h.Enter("run " + tc.name) })
		working := tui.ProgramStatus{App: icodingagent.AppName, State: tui.ProgramStateWorking}
		waitFor := func(want tui.ProgramStatus) []tui.ProgramStatus {
			t.Helper()
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				got := reports()[start:]
				if len(got) > 0 && got[len(got)-1] == want {
					return got
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s run: program status reports %+v, want working then %+v", tc.name, got, want)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if tc.outcome.StopReason == ai.StopReasonAborted {
			waitFor(working)
			h.Do(func() { h.Key("\x1b") })
		}
		got := waitFor(tc.want)
		if !slices.Contains(got, working) {
			t.Fatalf("%s run: program status reports %+v, want working before the outcome", tc.name, got)
		}
		h.WaitIdle(t, testbudget.Wait(t))
	}
}
