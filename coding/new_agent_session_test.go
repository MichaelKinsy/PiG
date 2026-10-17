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
	t.Cleanup(func() { _ = session.Close() })
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

// upstream: agent-session.ts:500 `this._usesDefaultTools = config.usesDefaultTools ?? false`: a session built with `new AgentSession(config)` uses the defaultTools setting only when its config says so; only createAgentSession derives the flag (sdk.ts:472), which NewSession does for the CLI runtime.
func TestNewAgentSessionUsesDefaultToolsOnlyWhenTheConfigSaysSo(t *testing.T) {
	_, services := newAvailabilitySession(t)
	for name, tc := range map[string]struct {
		stated *bool
		want   bool
	}{"omitted": {nil, false}, "true": {new(true), true}, "false": {new(false), false}} {
		session, err := NewAgentSession(SessionOptions{Services: services, NoSession: true, UsesDefaultTools: tc.stated})
		if err != nil {
			t.Fatal(err)
		}
		if got := session.toolRegistry.usesDefaultTools; got != tc.want {
			t.Errorf("%s: usesDefaultTools = %v, want %v", name, got, tc.want)
		}
		_ = session.Close()
	}
	// NewSession, the createAgentSession path, derives it: no tool list and no noTools uses the setting.
	session, err := NewSession(services, SessionOptions{NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if !session.toolRegistry.usesDefaultTools {
		t.Fatal("NewSession without a tool list does not use the defaultTools setting")
	}
}
