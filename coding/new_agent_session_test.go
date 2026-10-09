package coding

import (
	"errors"
	"slices"
	"testing"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:485-494: `new AgentSession(config)` builds the session from the one config object, taking cwd, settingsManager, resourceLoader and modelRuntime from it.
func TestNewAgentSessionBuildsTheSessionFromOneConfig(t *testing.T) {
	_, services := newAvailabilitySession(t)
	session, err := NewAgentSession(SessionOptions{Services: services, NoSession: true, InitialActiveToolNames: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Services() != services {
		t.Fatal("the Session was not built against config.Services")
	}
	if got := session.ActiveToolNames(); !slices.Equal(got, []string{"read"}) {
		t.Fatalf("active tools = %q, want the config's [read]", got)
	}
	if _, err := NewAgentSession(SessionOptions{NoSession: true}); !errors.Is(err, ErrNoServices) {
		t.Fatalf("a config without services = %v, want ErrNoServices", err)
	}
}
