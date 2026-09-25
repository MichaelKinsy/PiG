package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestGetAllTools_IncludesBuiltinTools verifies that the in-proc
// GetAllTools callback returns built-in tool names (bash, read, write,
// edit, etc.) alongside extension tools. Without this, the piglet
// extension's applyScoping cannot see built-in tools, causing them to be
// excluded from AllowedTools after a /reload or rescope: which is the
// root cause of the "Tool bash not found" bug.
func TestGetAllTools_IncludesBuiltinTools(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "test-ext",
		Tools: map[string]extension.RegisteredTool{
			"ext_tool": {Definition: extension.ToolDefinition{Name: "ext_tool", Description: "test"}, SourceInfo: "test-ext"},
		},
	}}, t.TempDir())

	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts: InteractiveOptions{
			CWD: t.TempDir(),
		},
	}
	m.wireInprocContextActions()

	ctx := runner.CreateCommandContext()
	allTools := ctx.GetAllTools()

	names := make([]string, len(allTools))
	for i, t := range allTools {
		names[i] = t.Name
	}

	// Built-in tools must be present; the opt-in powershell tool is not
	// active by default (upstream registers it but no default selects it).
	for _, want := range defaultActiveBuiltinNames {
		if !slices.Contains(names, want) {
			t.Errorf("GetAllTools() missing built-in tool %q, got: %v", want, names)
		}
	}
	if slices.Contains(names, "powershell") {
		t.Errorf("GetAllTools() lists the opt-in powershell tool by default: %v", names)
	}

	// Extension tool must also be present.
	if !slices.Contains(names, "ext_tool") {
		t.Errorf("GetAllTools() missing extension tool %q, got: %v", "ext_tool", names)
	}

	// Built-in tools must have source "builtin".
	for _, ti := range allTools {
		if slices.Contains(tools.BuiltinToolNames(), ti.Name) {
			source, _ := ti.SourceInfo.(string)
			if source != "builtin" {
				t.Errorf("built-in tool %q has source %q, want %q", ti.Name, source, "builtin")
			}
		}
	}
}

// TestGetActiveTools_IncludesBuiltinTools verifies that GetActiveTools
// also returns built-in tool names, so the intersection logic in
// applyScoping works correctly (len(currentActive) == len(allTools)
// → else branch → active = pigletScoped, which includes builtins).
func TestGetActiveTools_IncludesBuiltinTools(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "test-ext",
		Tools: map[string]extension.RegisteredTool{
			"ext_tool": {Definition: extension.ToolDefinition{Name: "ext_tool", Description: "test"}, SourceInfo: "test-ext"},
		},
	}}, t.TempDir())

	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts: InteractiveOptions{
			CWD: t.TempDir(),
		},
	}
	m.wireInprocContextActions()

	ctx := runner.CreateCommandContext()
	active := ctx.GetActiveTools()

	for _, want := range defaultActiveBuiltinNames {
		if !slices.Contains(active, want) {
			t.Errorf("GetActiveTools() missing built-in tool %q, got: %v", want, active)
		}
	}
	if slices.Contains(active, "powershell") {
		t.Errorf("GetActiveTools() lists the opt-in powershell tool by default: %v", active)
	}
}

// defaultActiveBuiltinNames is PiG's full default built-in set: every
// registered built-in except the opt-in powershell tool.
var defaultActiveBuiltinNames = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

// An allowlist that names powershell (for example --tools powershell)
// activates it, and refreshAgentTools then creates it for the agent.
func TestPowerShellToolActivatedByAllowlist(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		agent:     agent.NewAgent(agent.AgentOptions{}),
		opts: InteractiveOptions{
			CWD:          t.TempDir(),
			AllowedTools: map[string]struct{}{"powershell": {}, "read": {}},
		},
	}
	m.wireInprocContextActions()

	if active := runner.CreateCommandContext().GetActiveTools(); !slices.Contains(active, "powershell") {
		t.Fatalf("GetActiveTools() = %v, want powershell active", active)
	}
	m.refreshAgentTools()
	var names []string
	for _, tool := range m.agent.Tools() {
		names = append(names, tool.Name())
	}
	if !slices.Equal(names, []string{"read", "powershell"}) {
		t.Fatalf("agent tools = %v, want [read powershell]", names)
	}
}

// TestGetAllTools_RespectsActiveBuiltinTools verifies that when
// ActiveBuiltinTools is set (e.g., {read, bash, edit, write} by
// default), only those built-in tools are returned by GetAllTools -
// not the full set of 7. This ensures ScopeTools sees exactly the
// built-in tools that refreshAgentTools will create.
func TestGetAllTools_RespectsActiveBuiltinTools(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())

	activeBuiltin := map[string]struct{}{
		"read": {}, "bash": {}, "edit": {}, "write": {},
	}
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts: InteractiveOptions{
			CWD:                t.TempDir(),
			ActiveBuiltinTools: activeBuiltin,
		},
	}
	m.wireInprocContextActions()

	ctx := runner.CreateCommandContext()
	allTools := ctx.GetAllTools()

	names := make([]string, len(allTools))
	for i, t := range allTools {
		names[i] = t.Name
	}

	// Active built-in tools must be present.
	for _, want := range []string{"read", "bash", "edit", "write"} {
		if !slices.Contains(names, want) {
			t.Errorf("GetAllTools() missing active built-in tool %q, got: %v", want, names)
		}
	}

	// Inactive built-in tools must NOT be present.
	for _, unwanted := range []string{"grep", "find", "ls"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("GetAllTools() should not include inactive built-in tool %q, got: %v", unwanted, names)
		}
	}
}

// TestGetAllTools_AllBuiltinToolsWhenActiveBuiltinNil verifies that
// when ActiveBuiltinTools is nil, all 7 default built-in tools are returned
// and the opt-in powershell tool is not.
func TestGetAllTools_AllBuiltinToolsWhenActiveBuiltinNil(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())

	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts: InteractiveOptions{
			CWD: t.TempDir(),
			// ActiveBuiltinTools is nil → all builtins active
		},
	}
	m.wireInprocContextActions()

	ctx := runner.CreateCommandContext()
	allTools := ctx.GetAllTools()

	names := make([]string, len(allTools))
	for i, t := range allTools {
		names[i] = t.Name
	}

	if !slices.Equal(names, defaultActiveBuiltinNames) {
		t.Errorf("GetAllTools() = %v, want %v", names, defaultActiveBuiltinNames)
	}
}

// TestSetActiveTools_PreservesBuiltinToolsAfterScoping verifies the
// full applyScoping → SetActiveTools → refreshAgentTools flow: after
// SetActiveTools is called with a list that includes built-in tool
// names, refreshAgentTools should keep those built-in tools in the
// agent's tool set (not filter them out).
//
// This is the regression test for the "Tool bash not found" bug: before
// the fix, GetAllTools returned only extension tools, so ScopeTools
// built a list without builtins, and SetActiveTools set AllowedTools
// to that list: causing refreshAgentTools to filter out bash/read/
// write/edit.
func TestSetActiveTools_PreservesBuiltinToolsAfterScoping(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "test-ext",
		Tools: map[string]extension.RegisteredTool{
			"ext_tool": {Definition: extension.ToolDefinition{Name: "ext_tool", Description: "test"}, SourceInfo: "test-ext"},
		},
	}}, t.TempDir())

	activeBuiltin := map[string]struct{}{
		"read": {}, "bash": {}, "edit": {}, "write": {},
	}
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts: InteractiveOptions{
			CWD:                t.TempDir(),
			Settings:           Settings{},
			ActiveBuiltinTools: activeBuiltin,
			BridgeExtensionTools: func(rts []extension.RegisteredTool) ([]agent.AgentTool, []error) {
				result := make([]agent.AgentTool, 0, len(rts))
				for _, rt := range rts {
					result = append(result, namedTool{name: rt.Definition.Name})
				}
				return result, nil
			},
		},
		agent: agent.NewAgent(agent.AgentOptions{}),
	}
	m.wireInprocContextActions()

	// Simulate what the piglet extension's applyScoping does:
	// 1. Get all tools (now includes builtins)
	ctx := runner.CreateCommandContext()
	allTools := ctx.GetAllTools()

	// 2. Build the scoped list (simulating builtin: tools: all → keep all builtins)
	var scopedNames []string
	for _, ti := range allTools {
		scopedNames = append(scopedNames, ti.Name)
	}

	// 3. SetActiveTools (this calls refreshAgentTools internally)
	ctx.SetActiveTools(scopedNames)

	// 4. Verify built-in tools are in the agent's tool set
	agentTools := m.agent.Tools()
	agentToolNames := make([]string, len(agentTools))
	for i, at := range agentTools {
		agentToolNames[i] = at.Name()
	}

	for _, want := range []string{"bash", "read", "write", "edit"} {
		if !slices.Contains(agentToolNames, want) {
			t.Errorf("after SetActiveTools, agent tools missing built-in %q, got: %v", want, agentToolNames)
		}
	}

	// Extension tool should also be present.
	if !slices.Contains(agentToolNames, "ext_tool") {
		t.Errorf("after SetActiveTools, agent tools missing extension tool %q, got: %v", "ext_tool", agentToolNames)
	}
}

// TestGetAllTools_ExtensionOverrideSuppressesDuplicateBuiltin pins last-
// registration-wins semantics: an extension-owned tool named "bash" replaces
// the builtin and must appear exactly once with the extension source.
func TestGetAllTools_ExtensionOverrideSuppressesDuplicateBuiltin(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "override-ext",
		Tools: map[string]extension.RegisteredTool{
			"bash": {Definition: extension.ToolDefinition{Name: "bash", Description: "override"}, SourceInfo: "override-ext"},
		},
	}}, t.TempDir())
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		opts:      InteractiveOptions{CWD: t.TempDir()},
	}
	m.wireInprocContextActions()
	ctx := runner.CreateCommandContext()
	all := ctx.GetAllTools()
	count := 0
	for _, tool := range all {
		if tool.Name != "bash" {
			continue
		}
		count++
		if tool.SourceInfo != "override-ext" {
			t.Fatalf("bash source = %#v", tool.SourceInfo)
		}
	}
	if count != 1 {
		t.Fatalf("bash count = %d, tools = %#v", count, all)
	}
	active := ctx.GetActiveTools()
	count = 0
	for _, name := range active {
		if name == "bash" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("active bash count = %d, active = %v", count, active)
	}
}

func TestSubprocessGetAllTools_ExtensionOverrideSuppressesDuplicateBuiltin(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "override-ext",
		Tools: map[string]extension.RegisteredTool{
			"bash": {Definition: extension.ToolDefinition{Name: "bash", Description: "override"}, SourceInfo: "override-ext"},
		},
	}}, t.TempDir())
	bridge := &captureUIBridge{}
	m := &InteractiveMode{
		newRunner: runner,
		opts:      InteractiveOptions{CWD: t.TempDir(), SubprocessUIBridge: bridge},
	}
	m.wireSubprocessHostCallbacks()
	getAll, ok := bridge.actions["getAllTools"].(func() []map[string]string)
	if !ok {
		t.Fatalf("getAllTools action = %T", bridge.actions["getAllTools"])
	}
	count := 0
	for _, tool := range getAll() {
		if tool["name"] != "bash" {
			continue
		}
		count++
		if tool["source"] != "override-ext" {
			t.Fatalf("bash source = %q", tool["source"])
		}
	}
	if count != 1 {
		t.Fatalf("bash count = %d", count)
	}
}
