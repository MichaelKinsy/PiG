package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// BenchmarkRuntimeNewSession includes cwd-bound Services construction and joins the outgoing Session's event consumer before constructing the next one.
func BenchmarkRuntimeNewSession(b *testing.B) {
	cwd, agentDir := b.TempDir(), b.TempDir()
	var drained <-chan struct{}
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		if drained != nil {
			<-drained
		}
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: options.CWD, AgentDir: options.AgentDir})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, Model: fakeModel(), Runner: inproc.NewRunner(nil, options.CWD)})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		done := make(chan struct{})
		drained = done
		go func() {
			defer close(done)
			for event := range session.Events() {
				AcknowledgeEvent(event)
			}
		}()
		return CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}
	manager, err := NewInMemorySessionManager(cwd)
	if err != nil {
		b.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(b.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = runtime.Close(); <-drained }()
	b.ReportAllocs()
	for b.Loop() {
		result, err := runtime.NewSession(b.Context(), nil)
		if err != nil || result.Cancelled {
			b.Fatalf("new Session=%v error=%v", result, err)
		}
	}
}
