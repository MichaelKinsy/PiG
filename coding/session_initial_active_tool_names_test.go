package coding

import (
	"slices"
	"testing"
)

// The tools a `--tools` list names are active in the caller's order, which orders the tools sent to the provider and listed in the system prompt; the tools the allowlist matches but the list does not name follow in registry order.
//
// mutation-checked: dropping the InitialActiveToolNames prefix in session_tool_registry.go gave registry order ([read bash] and [read bash grep]) for a list that names bash before read.
// upstream: packages/coding-agent/src/core/sdk.ts:272-276 (initialActiveToolNames = options.tools in the caller's order), agent-session.ts:3552-3554 (activates them in that order, then the tools allowedTools matches)
func TestNewSessionActivatesInitialActiveToolNamesInTheCallersOrder(t *testing.T) {
	svcs := newTestServices(t)
	for _, tc := range []struct {
		name    string
		allowed map[string]struct{}
		initial []string
		want    []string
	}{
		{"list order", map[string]struct{}{"bash": {}, "read": {}}, []string{"bash", "read"}, []string{"bash", "read"}},
		{"unlisted allowlist match follows", map[string]struct{}{"bash": {}, "read": {}, "g*": {}}, []string{"bash", "read", "g*"}, []string{"bash", "read", "grep"}},
		{"no list keeps registry order", map[string]struct{}{"bash": {}, "read": {}}, nil, []string{"read", "bash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess, err := NewSession(svcs, SessionOptions{Model: fakeModel(), AllowedTools: tc.allowed, InitialActiveToolNames: tc.initial})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()
			if got := toolNames(sess.Tools()); !slices.Equal(got, tc.want) {
				t.Errorf("tools = %v, want %v", got, tc.want)
			}
		})
	}
}

// Pi filters initialActiveToolNames by excludeTools (sdk.ts:274-276) and isAllowedTool (agent-session.ts:3552-3554): `--tools read,bash --exclude-tools bash` leaves bash inactive, and a name the allowlist does not admit never activates.
//
// mutation-checked: skipping the registry's exclusion filter (ExcludedTools ignored) left bash active.
// upstream: packages/coding-agent/src/core/sdk.ts:274-276, agent-session.ts:3552-3554
func TestNewSessionInitialActiveToolNamesHonourTheExclusionList(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{
		Model:                  fakeModel(),
		AllowedTools:           map[string]struct{}{"read": {}, "bash": {}},
		InitialActiveToolNames: []string{"bash", "read", "edit"},
		ExcludedTools:          map[string]struct{}{"bash": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if got := toolNames(sess.Tools()); !slices.Equal(got, []string{"read"}) {
		t.Errorf("tools = %v, want [read]: bash is excluded and edit is not allowed", got)
	}
}
