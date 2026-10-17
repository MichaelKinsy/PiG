package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/test/default-tools-setting.test.ts "keeps tools removed by -name tool options removed on reload"
// (agent-session.ts:272-278 defaultToolModifiers, :3592-3609 reload). The `+name`/`-name` entries of the tools option apply on top of the
// reloaded defaultTools setting, so a tool the option removed stays removed and only tools the modified list newly gains activate.
func TestReloadAppliesDefaultToolModifiersToTheReloadedSetting(t *testing.T) {
	reloadTo := func(t *testing.T, modifiers []string) []string {
		t.Helper()
		session := newRegistryPortSession(t, nil, SessionOptions{
			InitialActiveToolNames: []string{"read", "grep"},
			DefaultToolModifiers:   modifiers,
		}, []extension.ToolDefinition{inactiveRegistryTool()}, nil)
		bindRegistryPort(t, session)
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"read"}})
		reloadRegistryPort(t, session)
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"read", "bash", "inactive_tool"}})
		reloadRegistryPort(t, session)
		return sortedActiveNames(session)
	}
	if got, want := reloadTo(t, []string{"-bash", "+grep"}), []string{"grep", "inactive_tool", "read"}; !slices.Equal(got, want) {
		t.Fatalf("with -bash +grep, active = %q, want %q", got, want)
	}
	// Without the modifiers the setting's bash is newly added and activates.
	if got := reloadTo(t, nil); !slices.Contains(got, "bash") {
		t.Fatalf("without modifiers, active = %q, want bash newly active", got)
	}
}

// Pi: packages/coding-agent/test/default-tools-setting.test.ts:187-195 "rejects invalid tool modifier options" (sdk.ts:280-281): a modifier list that mixes plain names with +name/-name entries, or gives a modifier a pattern, fails
// session creation with "Invalid tools option: ...".
func TestNewSessionRejectsInvalidToolModifierOptions(t *testing.T) {
	for _, tc := range []struct {
		modifiers []string
		want      string
	}{
		{[]string{"read", "+grep"}, "Invalid tools option: tool names cannot be mixed with +name or -name entries"},
		{[]string{"-gr*"}, "Invalid tools option: +name and -name entries take exact tool names, not patterns: -gr*"},
	} {
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewSession(services, SessionOptions{Model: fakeModel(), SessionDir: t.TempDir(), DefaultToolModifiers: tc.modifiers})
		if err == nil || err.Error() != tc.want {
			t.Errorf("modifiers %v: error = %v, want %q", tc.modifiers, err, tc.want)
		}
	}
	// Go carries the names of a mixed list as AllowedTools beside the modifiers: the same rejection.
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewSession(services, SessionOptions{Model: fakeModel(), SessionDir: t.TempDir(), AllowedTools: map[string]struct{}{"read": {}}, DefaultToolModifiers: []string{"+grep"}})
	if want := "Invalid tools option: tool names cannot be mixed with +name or -name entries"; err == nil || err.Error() != want {
		t.Errorf("allowlist beside modifiers: error = %v, want %q", err, want)
	}
}
