package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's AgentSessionRuntime reads diagnostics and modelFallbackMessage from the factory result and replaces both when a replacement applies its own result (agent-session-runtime.ts apply).
// Pi: packages/coding-agent/src/core/agent-session-runtime.ts:109 (Runtime.diagnostics); packages/coding-agent/src/core/agent-session-runtime.ts:113 (CreateAgentSessionRuntimeResult.modelFallbackMessage); packages/coding-agent/src/core/agent-session-runtime.ts:113 (Runtime.modelFallbackMessage); packages/coding-agent/src/core/agent-session-runtime.ts:25 (CreateAgentSessionRuntimeResult.diagnostics).
func TestRuntimeKeepsDiagnosticsAndModelFallbackOfTheCurrentConstruction(t *testing.T) {
	services := newTestServices(t)
	built := 0
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		built++
		session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, Model: fakeModel(), Runner: inproc.NewRunner(nil, services.CWD())})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		t.Cleanup(func() { _ = session.Close() })
		result := CreateAgentSessionRuntimeResult{Session: session, Services: services}
		if built == 1 {
			result.Diagnostics = []icodingagent.AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "first"}}
			result.ModelFallbackMessage = "Could not restore model a/b. Using c/d"
		}
		return result, nil
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
	if want := []icodingagent.AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "first"}}; !reflect.DeepEqual(runtime.Diagnostics(), want) {
		t.Fatalf("diagnostics=%v want=%v", runtime.Diagnostics(), want)
	}
	if got := runtime.ModelFallbackMessage(); got != "Could not restore model a/b. Using c/d" {
		t.Fatalf("model fallback=%q", got)
	}
	if _, err := runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Diagnostics()) != 0 || runtime.ModelFallbackMessage() != "" {
		t.Fatalf("replacement kept the outgoing construction: %v %q", runtime.Diagnostics(), runtime.ModelFallbackMessage())
	}
}
