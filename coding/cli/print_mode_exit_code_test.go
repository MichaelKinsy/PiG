package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// print-mode.ts:35,158,161 runPrintMode returns 0 after a completed run and 1 when the run failed; PiG returns the code with the error that says what failed. The
// runtime the run owns is disposed by it (runtimeHost.dispose, :158 finally), so the runtime's teardown hook fires once on either path.
func TestRunPrintModeReturnsExitCodeZeroOnSuccessAndOneOnFailureAndDisposesTheRuntime(t *testing.T) {
	run := func(t *testing.T, mode string, convert func(agent.AgentEvent) ([]any, error)) (int, string, *int, error) {
		t.Helper()
		provider := ai.NewFauxProvider(ai.FauxConfig{})
		provider.SetResponses(fauxSteps(fauxTextResponse("the answer")))
		host := printModeTestHost(t, provider)
		factory := newCLISessionFactory(context.Background(), host.inputs(), struct{}{}, nil)
		t.Cleanup(factory.Close)
		manager, err := coding.SessionManagerFor(host.Services, host.startOptions())
		if err != nil {
			t.Fatal(err)
		}
		rt, err := coding.CreateAgentSessionRuntime(context.Background(), factory.Factory(), coding.CreateAgentSessionRuntimeOptions{
			CWD: host.Services.CWD(), AgentDir: host.Services.AgentDir(), SessionManager: manager,
		})
		if err != nil {
			t.Fatal(err)
		}
		disposed := new(int)
		rt.SetBeforeSessionInvalidate(func() { *disposed++ })
		var stdout, stderr bytes.Buffer
		code, err := runPrintMode(context.Background(), rt, printModeOptions{Mode: mode, InitialMessage: "go", stdout: &stdout, stderr: &stderr, convertEvent: convert})
		return code, stdout.String(), disposed, err
	}

	code, stdout, disposed, err := run(t, "text", nil)
	if code != 0 || err != nil || !strings.Contains(stdout, "the answer") {
		t.Errorf("a completed run returned exit code %d, error %v, stdout %q; want 0, nil and the answer", code, err, stdout)
	}
	if *disposed != 1 {
		t.Errorf("the completed run disposed its runtime %d times, want once", *disposed)
	}

	failure := errors.New("the event could not be written")
	code, _, disposed, err = run(t, "json", func(ev agent.AgentEvent) ([]any, error) {
		if _, ok := ev.(agent.MessageEndEvent); ok {
			return nil, failure
		}
		return rpcAgentEvent(ev)
	})
	if code != 1 || err == nil {
		t.Errorf("a failed run returned exit code %d, error %v; want 1 and an error", code, err)
	}
	if *disposed != 1 {
		t.Errorf("the failed run disposed its runtime %d times, want once", *disposed)
	}
}
