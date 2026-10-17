package coding

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/default-tools-setting.test.ts (v1.1.0, ddaa0a03): the SDK `tools` option with only `+name` and `-name` entries.

func activeRegistryTool() extension.ToolDefinition {
	return registryTool("active_tool", "Active Tool", "Extension tool registered active", "")
}

// default-tools-setting.test.ts "applies +name and -name tool options to the default selection".
func TestToolModifierOptionsChangeTheDefaultSelectionPort(t *testing.T) {
	static := []extension.ToolDefinition{inactiveRegistryTool(), activeRegistryTool()}
	session := newRegistryPortSession(t, []string{"+grep"}, SessionOptions{DefaultToolModifiers: []string{"+inactive_tool", "-write"}}, static, nil)
	bindRegistryPort(t, session)
	if got, want := sortedActiveNames(session), []string{"active_tool", "bash", "edit", "grep", "inactive_tool", "read"}; !slices.Equal(got, want) {
		t.Fatalf("active = %q, want %q", got, want)
	}

	toolLess := newRegistryPortSession(t, []string{"read"}, SessionOptions{NoTools: "all", DefaultToolModifiers: []string{"+inactive_tool"}}, static, nil)
	bindRegistryPort(t, toolLess)
	if got, want := toolLess.ActiveToolNames(), []string{"inactive_tool"}; !slices.Equal(got, want) {
		t.Fatalf("noTools all active = %q, want %q", got, want)
	}

	// sdk.ts: noTools "builtin" starts the selection from no tools, and the modifiers still add to it.
	builtinless := newRegistryPortSession(t, []string{"read"}, SessionOptions{NoTools: "builtin", DefaultToolModifiers: []string{"+grep", "-read"}}, static, nil)
	bindRegistryPort(t, builtinless)
	if got, want := sortedActiveNames(builtinless), []string{"active_tool", "grep"}; !slices.Equal(got, want) {
		t.Fatalf("noTools builtin active = %q, want %q", got, want)
	}

	// --exclude-tools applies after the modifiers (sdk.ts initialActiveToolNames).
	excluded := newRegistryPortSession(t, nil, SessionOptions{DefaultToolModifiers: []string{"+grep"}, ExcludedTools: map[string]struct{}{"grep": {}}}, nil, nil)
	if got, want := excluded.ActiveToolNames(), []string{"read", "bash", "edit", "write"}; !slices.Equal(got, want) {
		t.Fatalf("excluded active = %q, want %q", got, want)
	}
}

// default-tools-setting.test.ts "rejects invalid tool modifier options".
func TestInvalidToolModifierOptionsPort(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tools []string
		want  string
	}{
		{[]string{"read", "+grep"}, "Invalid tools option: tool names cannot be mixed with +name or -name entries"},
		{[]string{"-gr*"}, "Invalid tools option: +name and -name entries take exact tool names, not patterns: -gr*"},
	} {
		_, err := NewSession(services, SessionOptions{Model: fakeModel(), SessionDir: filepath.Join(agentDir, "sessions"), DefaultToolModifiers: tc.tools})
		if err == nil || err.Error() != tc.want {
			t.Errorf("tools %q: error = %v, want %q", tc.tools, err, tc.want)
		}
	}
}

// default-tools-setting.test.ts reload "keeps tools removed by -name tool options removed on reload".
func TestToolModifierOptionsSurviveReloadPort(t *testing.T) {
	session := newRegistryPortSession(t, []string{"read"}, SessionOptions{DefaultToolModifiers: []string{"-bash", "+grep"}}, []extension.ToolDefinition{inactiveRegistryTool()}, nil)
	bindRegistryPort(t, session)
	if got, want := session.ActiveToolNames(), []string{"read", "grep"}; !slices.Equal(got, want) {
		t.Fatalf("initial active = %q, want %q", got, want)
	}

	writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"read", "bash", "inactive_tool"}})
	reloadRegistryPort(t, session)
	if got, want := sortedActiveNames(session), []string{"grep", "inactive_tool", "read"}; !slices.Equal(got, want) {
		t.Fatalf("active after reload = %q, want %q", got, want)
	}
}

// sdk.ts:274-276: an explicit initial list outranks noTools (initialActiveToolNames = tools ?? (noTools ? [] : defaults)), so `--no-builtin-tools --tools +grep` activates the built-in grep, while the Session still does not treat the list as the defaultTools setting (sdk.ts:472 usesDefaultTools).
func TestInitialActiveToolNamesOutrankNoToolsBuiltin(t *testing.T) {
	session := newRegistryPortSession(t, nil, SessionOptions{NoTools: "builtin", InitialActiveToolNames: []string{"grep"}, DefaultToolModifiers: []string{"+grep"}}, nil, nil)
	if got, want := session.ActiveToolNames(), []string{"grep"}; !slices.Equal(got, want) {
		t.Fatalf("active = %q, want %q", got, want)
	}
	if session.toolRegistry.usesDefaultTools {
		t.Fatal("usesDefaultTools is set under noTools builtin")
	}
	none := newRegistryPortSession(t, nil, SessionOptions{NoTools: "builtin"}, nil, nil)
	if got := none.ActiveToolNames(); len(got) != 0 {
		t.Fatalf("noTools builtin without a list activates %q", got)
	}
}
