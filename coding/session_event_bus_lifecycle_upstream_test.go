package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// packages/coding-agent/test/suite/regressions/7193-event-bus-lifecycle.test.ts:67 and agent-session.ts:dispose. Closing a Session directly must invalidate retained extension contexts, not only closing its outer Runtime.
func TestSessionCloseInvalidatesExtensionContext7193(t *testing.T) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runner := inproc.NewRunner(nil, services.CWD())
	session, err := NewSession(services, SessionOptions{Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	captured := runner.CreateCommandContext()
	if _, err := captured.CWD(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := captured.CWD(); err == nil || !strings.Contains(err.Error(), "stale after session replacement or reload") {
		t.Fatalf("retained extension context after Session.Close: %v", err)
	}
}
