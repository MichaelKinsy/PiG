package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi: packages/coding-agent/test/agent-session-runtime-diagnostics.test.ts, describe "AgentSessionRuntime.takeUnreportedDiagnostics".
// A runtime whose factory returns whatever nextDiagnostics holds when it runs.
func newRuntimeReturningDiagnostics(t *testing.T, nextDiagnostics *[]AgentSessionRuntimeDiagnostic) *Runtime {
	t.Helper()
	services := newTestServices(t)
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, Model: fakeModel(), Runner: inproc.NewRunner(nil, services.CWD())})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		t.Cleanup(func() { _ = session.Close() })
		return CreateAgentSessionRuntimeResult{Session: session, Services: services, Diagnostics: *nextDiagnostics}, nil
	}
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

// The runtime factory runs again on every session replacement, but modes only reported the startup diagnostics, so a problem that first appeared on /new or /resume was never shown.
// Pi: packages/coding-agent/test/agent-session-runtime-diagnostics.test.ts "returns each diagnostic once, at startup and after replacements"; packages/coding-agent/src/core/agent-session-runtime.ts takeUnreportedDiagnostics.
func TestTakeUnreportedDiagnosticsReturnsEachDiagnosticOnceAtStartupAndAfterReplacements(t *testing.T) {
	settingsWarning := AgentSessionRuntimeDiagnostic{Type: "warning", Message: "settings warning"}
	extensionError := AgentSessionRuntimeDiagnostic{Type: "error", Message: "extension failed"}
	next := []AgentSessionRuntimeDiagnostic{settingsWarning}
	runtime := newRuntimeReturningDiagnostics(t, &next)
	expect := func(step string, want []AgentSessionRuntimeDiagnostic) {
		t.Helper()
		got := runtime.TakeUnreportedDiagnostics()
		if len(got) == 0 && len(want) == 0 {
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: took %v, want %v", step, got, want)
		}
	}

	expect("startup", []AgentSessionRuntimeDiagnostic{settingsWarning})
	expect("startup again", nil)

	next = []AgentSessionRuntimeDiagnostic{settingsWarning, extensionError}
	if _, err := runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	expect("after the first new session", []AgentSessionRuntimeDiagnostic{extensionError})
	expect("after the first new session again", nil)

	if _, err := runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	expect("after the second new session", nil)
}

// A diagnostic that is fixed and then reappears with the same text is not returned again: the reported set belongs to the runtime, not to one construction (Pi's commit message: "that seemed preferable to repeating every warning").
// Pi: packages/coding-agent/src/core/agent-session-runtime.ts reportedDiagnosticKeys.
func TestTakeUnreportedDiagnosticsDoesNotRepeatAFixedAndReappearingDiagnostic(t *testing.T) {
	warning := AgentSessionRuntimeDiagnostic{Type: "warning", Message: "w"}
	next := []AgentSessionRuntimeDiagnostic{warning}
	runtime := newRuntimeReturningDiagnostics(t, &next)
	if got := runtime.TakeUnreportedDiagnostics(); !reflect.DeepEqual(got, []AgentSessionRuntimeDiagnostic{warning}) {
		t.Fatalf("startup took %v", got)
	}
	next = nil
	if _, err := runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	next = []AgentSessionRuntimeDiagnostic{warning}
	if _, err := runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := runtime.TakeUnreportedDiagnostics(); len(got) != 0 {
		t.Fatalf("a reappearing diagnostic was returned again: %v", got)
	}
}

// Type and message make one diagnostic: the same message with another type is a new diagnostic.
// Pi: `${diagnostic.type}\0${diagnostic.message}`.
func TestTakeUnreportedDiagnosticsKeysByTypeAndMessage(t *testing.T) {
	next := []AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "same"}, {Type: "error", Message: "same"}, {Type: "warning", Message: "same"}}
	runtime := newRuntimeReturningDiagnostics(t, &next)
	want := []AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "same"}, {Type: "error", Message: "same"}}
	if got := runtime.TakeUnreportedDiagnostics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("took %v, want %v", got, want)
	}
}
