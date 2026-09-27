package codingagent

import (
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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

	m.refreshAgentTools()
	var names []string
	for _, tool := range m.agent.Tools() {
		names = append(names, tool.Name())
	}
	if !slices.Equal(names, []string{"read", "powershell"}) {
		t.Fatalf("agent tools = %v, want [read powershell]", names)
	}
	if active := runner.CreateCommandContext().GetActiveTools(); !slices.Equal(active, names) {
		t.Fatalf("GetActiveTools() = %v, want the agent's tools %v", active, names)
	}
}

// Upstream getActiveTools reports the agent's tools (getActiveToolNames), so
// a tool an extension deactivated stays off across a getActiveTools and
// setActiveTools round trip. pi-lens deactivates its lazy tools at
// session_start and later merges additions into getActiveTools().
func TestActiveToolsReportTheAgentLoadout(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "lazy-ext",
		Tools: map[string]extension.RegisteredTool{
			"lazy_tool": {Definition: extension.ToolDefinition{Name: "lazy_tool", Description: "lazy"}, SourceInfo: "lazy-ext"},
		},
	}}, t.TempDir())
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
		agent:     agent.NewAgent(agent.AgentOptions{}),
		opts: InteractiveOptions{
			CWD:                t.TempDir(),
			ActiveBuiltinTools: map[string]struct{}{"read": {}},
			BridgeExtensionTools: func(registered []extension.RegisteredTool) ([]agent.AgentTool, []error) {
				var bridged []agent.AgentTool
				for _, tool := range registered {
					bridged = append(bridged, namedTool{name: tool.Definition.Name})
				}
				return bridged, nil
			},
		},
	}
	m.wireInprocContextActions()
	m.refreshAgentTools()
	ctx := runner.CreateCommandContext()
	if active := ctx.GetActiveTools(); !slices.Equal(active, []string{"read", "lazy_tool"}) {
		t.Fatalf("GetActiveTools() = %v, want [read lazy_tool]", active)
	}
	ctx.SetActiveTools([]string{"read"})
	if active := ctx.GetActiveTools(); slices.Contains(active, "lazy_tool") {
		t.Fatalf("GetActiveTools() = %v after deactivating lazy_tool", active)
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

// The subprocess getAllTools mirrors upstream AgentSession.getAllTools: every
// admitted built-in, active or not, in createAllToolDefinitions order, and an
// extension tool named like a built-in takes that built-in's place with the
// extension's definition and sourceInfo.
func TestSubprocessGetAllTools_ExtensionOverrideReplacesBuiltinInPlace(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name:       "override-ext",
		SourceInfo: PiSourceInfo{Path: "/ext/override.ts", Source: "cli", Scope: "temporary", Origin: "top-level"},
		Tools: map[string]extension.RegisteredTool{
			"bash":  {Definition: extension.ToolDefinition{Name: "bash", Description: "override", Parameters: json.RawMessage(`{"type":"object"}`)}, SourceInfo: "override-ext"},
			"extra": {Definition: extension.ToolDefinition{Name: "extra", Description: "extra tool", Parameters: json.RawMessage(`{"type":"object","properties":{}}`), PromptGuidelines: []string{"Use extra."}}, SourceInfo: "override-ext"},
		},
		ToolOrder: []string{"bash", "extra"},
	}}, t.TempDir())
	bridge := &captureUIBridge{}
	m := &InteractiveMode{
		newRunner: runner,
		opts:      InteractiveOptions{CWD: t.TempDir(), SubprocessUIBridge: bridge, ExcludedTools: map[string]struct{}{"ls": {}}},
	}
	m.wireSubprocessHostCallbacks()
	getAll, ok := bridge.actions["getAllTools"].(func() []subprocess.ToolInfo)
	if !ok {
		t.Fatalf("getAllTools action = %T", bridge.actions["getAllTools"])
	}
	tools := getAll()
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if want := []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "extra"}; !slices.Equal(names, want) {
		t.Fatalf("getAllTools names = %v, want %v", names, want)
	}
	bash := tools[1]
	if bash.Description != "override" || string(bash.Parameters) != `{"type":"object"}` ||
		bash.SourceInfo != (PiSourceInfo{Path: "/ext/override.ts", Source: "cli", Scope: "temporary", Origin: "top-level"}) {
		t.Fatalf("bash = %+v, want the extension's definition and sourceInfo", bash)
	}
	grep := tools[5]
	if grep.SourceInfo != (PiSourceInfo{Path: "<builtin:grep>", Source: "builtin", Scope: "temporary", Origin: "top-level"}) || len(grep.Parameters) == 0 {
		t.Fatalf("grep = %+v, want the built-in definition", grep)
	}
	if extra := tools[7]; !slices.Equal(extra.PromptGuidelines, []string{"Use extra."}) {
		t.Fatalf("extra = %+v", extra)
	}
}

// The subprocess getCommands mirrors upstream getCommands: extension
// commands, then prompt templates, then skills, each with its source and
// sourceInfo. It reads the catalog the owner loop last published.
func TestSubprocessGetCommands_ListsExtensionCommandsTemplatesAndSkills(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{
		Name:         "cmd-ext",
		SourceInfo:   PiSourceInfo{Path: "/ext/cmd.ts", Source: "cli", Scope: "temporary", Origin: "top-level"},
		Commands:     map[string]extension.RegisteredCommand{"probe": {Name: "probe", Description: "Probe"}},
		CommandOrder: []string{"probe"},
	}}, t.TempDir())
	bridge := &captureUIBridge{}
	agentDir := t.TempDir()
	templatePath := filepath.Join(agentDir, "prompts", "review.md")
	skillPath := filepath.Join(agentDir, "skills", "lint", "SKILL.md")
	m := &InteractiveMode{
		newRunner:       runner,
		promptTemplates: []PromptTemplate{{Name: "review", Description: "Review code", FilePath: templatePath}},
		opts: InteractiveOptions{CWD: t.TempDir(), AgentDir: agentDir, SubprocessUIBridge: bridge,
			Skills: []*SkillDef{{Name: "lint", Description: "Lint code", Path: skillPath}}},
	}
	m.wireSubprocessHostCallbacks()
	m.publishSlashCommandCatalog()
	getCommands, ok := bridge.actions["getCommands"].(func() []subprocess.CommandInfo)
	if !ok {
		t.Fatalf("getCommands action = %T", bridge.actions["getCommands"])
	}
	got, err := json.Marshal(getCommands())
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal([]subprocess.CommandInfo{
		{Name: "probe", Description: "Probe", Source: "extension", SourceInfo: PiSourceInfo{Path: "/ext/cmd.ts", Source: "cli", Scope: "temporary", Origin: "top-level"}},
		{Name: "review", Description: "Review code", Source: "prompt", SourceInfo: PiSourceInfo{Path: templatePath, Source: "local", Scope: "user", Origin: "top-level", BaseDir: filepath.Join(agentDir, "prompts")}},
		{Name: "skill:lint", Description: "Lint code", Source: "skill", SourceInfo: PiSourceInfo{Path: skillPath, Source: "local", Scope: "user", Origin: "top-level", BaseDir: filepath.Join(agentDir, "skills")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("getCommands = %s\nwant %s", got, want)
	}
}
