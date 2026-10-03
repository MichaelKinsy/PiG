package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Upstream runner.ts:889-891 (`get sessionManager()`) returns the SessionManager of the AgentSession the runner is bound to, and a replacement builds a new AgentSession and binds a new runner (agent-session-runtime.ts). Go's Session swaps its log in place with ReplaceInner, so the runner must follow: a context made after the swap exposes the new log, never the old one.
func TestReplaceInnerRebindsTheSessionManagerExtensionsSee(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	session, err := NewSession(warmingServices(t, "off"), SessionOptions{Model: warmingModel(&warmingProvider{}), Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	managerOf := func() any {
		t.Helper()
		manager, err := runner.CreateCommandContext().SessionManager()
		if err != nil {
			t.Fatal(err)
		}
		return manager
	}
	if managerOf() != any(session.Inner()) {
		t.Fatalf("ctx.sessionManager = %T, want the Session's own *codingagent.Session", managerOf())
	}
	replacement := icodingagent.NewSession("replacement", session.CWD())
	session.ReplaceInner(replacement)
	if managerOf() != any(replacement) {
		t.Fatalf("after ReplaceInner ctx.sessionManager is still the replaced log: %T %p, want %p", managerOf(), managerOf(), replacement)
	}
}
