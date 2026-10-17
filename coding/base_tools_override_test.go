package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func baseToolsOverrideSession(t *testing.T, opts SessionOptions) *Session {
	t.Helper()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	opts.Model = &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}}
	opts.NoSession = true
	session, err := NewSession(services, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// agent-session.ts:3614-3624, 3650-3652 (_buildRuntime): a base tools override replaces the built-in tools as the base set, and its keys are the
// tools active at start, in the record's order. (agent-session-concurrent.test.ts:430 and agent-session-retry.test.ts:309 build a session on
// `baseToolsOverride: { dummy: tool }` and run that tool.)
func TestBaseToolsOverrideReplacesTheBuiltInToolsAndIsActive(t *testing.T) {
	session := baseToolsOverrideSession(t, SessionOptions{BaseToolsOverride: []BaseToolOverride{
		{Name: "zeta", Tool: &nestedTestTool{name: "zeta"}},
		{Name: "alpha", Tool: &nestedTestTool{name: "alpha"}},
	}})
	if got, want := session.ActiveToolNames(), []string{"zeta", "alpha"}; !slices.Equal(got, want) {
		t.Fatalf("active tools = %v, want the override keys in record order %v", got, want)
	}
	var registered []string
	for _, info := range session.GetAllTools() {
		registered = append(registered, info.Name)
	}
	slices.Sort(registered)
	if want := []string{"alpha", "zeta"}; !slices.Equal(registered, want) {
		t.Fatalf("registered tools = %v, want only the override %v (no read, bash, edit or write)", registered, want)
	}
	// Without the override the session registers and activates the built-in tools.
	plain := baseToolsOverrideSession(t, SessionOptions{})
	if got := plain.ActiveToolNames(); !slices.Contains(got, "read") || slices.Contains(got, "zeta") {
		t.Fatalf("plain session active tools = %v", got)
	}
}

// agent-session.ts:3614-3620 synthesizes each override with tool-definition-wrapper.ts:46-58 createToolDefinitionFromAgentTool, which copies the tool's own fields and adds no prompt snippet: an override that reuses a built-in name is not listed under the built-in's snippet, and it keeps its own execution mode.
func TestBaseToolsOverrideIsAMinimalDefinitionWithoutTheBuiltInSnippet(t *testing.T) {
	session := baseToolsOverrideSession(t, SessionOptions{BaseToolsOverride: []BaseToolOverride{
		{Name: "read", Tool: &nestedTestTool{name: "read", mode: agent.ToolModeSequential}},
	}})
	if snippet, ok := session.GetSystemPromptOptions().ToolSnippets["read"]; ok {
		t.Fatalf("the override named read has the prompt snippet %q, want none (createToolDefinitionFromAgentTool sets no promptSnippet)", snippet)
	}
	if got := session.toolRegistry.base[0].registration.Definition.ExecutionMode; got != extension.ToolExecutionMode(agent.ToolModeSequential) {
		t.Fatalf("the override's definition execution mode = %q, want the tool's own %q", got, agent.ToolModeSequential)
	}
	// The built-in read tool keeps its snippet, so the assertion above fails for the right reason.
	if _, ok := baseToolsOverrideSession(t, SessionOptions{}).GetSystemPromptOptions().ToolSnippets["read"]; !ok {
		t.Fatal("the built-in read tool has no prompt snippet")
	}
}

// agent-session.ts:500 `config.usesDefaultTools ?? false`: a session built on a base tools override does not use the defaultTools setting, so a
// reload that adds a tool to it activates nothing; sdk.ts:472 derives it for createAgentSession, and the caller can state it.
func TestUsesDefaultToolsIsDerivedAndOverridable(t *testing.T) {
	yes, no := true, false
	override := []BaseToolOverride{{Name: "dummy", Tool: &nestedTestTool{name: "dummy"}}}
	for name, tc := range map[string]struct {
		opts SessionOptions
		want bool
	}{
		"createAgentSession default":  {SessionOptions{}, true},
		"override":                    {SessionOptions{BaseToolsOverride: override}, false},
		"override, stated":            {SessionOptions{BaseToolsOverride: override, UsesDefaultTools: &yes}, true},
		"default, stated off":         {SessionOptions{UsesDefaultTools: &no}, false},
		"noTools":                     {SessionOptions{NoTools: "all"}, false},
		"allowed tool list":           {SessionOptions{AllowedTools: map[string]struct{}{"read": {}}}, false},
		"override with allowed tools": {SessionOptions{BaseToolsOverride: override, AllowedTools: map[string]struct{}{}}, false},
	} {
		if got := usesDefaultTools(tc.opts); got != tc.want {
			t.Errorf("%s: usesDefaultTools = %v, want %v", name, got, tc.want)
		}
		session := baseToolsOverrideSession(t, tc.opts)
		if got := session.toolRegistry.usesDefaultTools; got != tc.want {
			t.Errorf("%s: the session's registry usesDefaultTools = %v, want %v", name, got, tc.want)
		}
	}
}
