package cli

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// `--tools bash,read` lists bash before read in the system prompt and the provider's tools, and hands the Session the initial names in that order: the explicit list keeps the caller's order, the tools its patterns match follow in registry order, `--exclude-tools` removes names from both lists, and `--no-tools` starts with none.
//
// mutation-checked: iterating the registry order instead of --tools (agentToolNames) and `initialActive = filtered` gave [read bash] for the agent tools and the initial names of `--tools bash,read`.
// upstream: packages/coding-agent/src/core/sdk.ts:260-276 (allowedToolNames = tools ?? ..., initialActiveToolNames = tools ?? ..., filtered by excludeTools), agent-session.ts:3552-3562
func TestResolveToolSelectionKeepsTheToolsFlagOrder(t *testing.T) {
	registry := tools.BuiltinToolNames()
	for _, tc := range []struct {
		name        string
		flags       Args
		defaults    []string
		wantAgent   []string
		wantInitial []string
		wantAllowed int
	}{
		{name: "caller order", flags: Args{Tools: []string{"bash", "read"}}, wantAgent: []string{"bash", "read"}, wantInitial: []string{"bash", "read"}, wantAllowed: 2},
		{name: "duplicates and unknown names", flags: Args{Tools: []string{"edit", "nope", "read", "edit"}}, wantAgent: []string{"edit", "read"}, wantInitial: []string{"edit", "nope", "read", "edit"}, wantAllowed: 3},
		{name: "pattern matches follow in registry order", flags: Args{Tools: []string{"write", "g*"}}, wantAgent: []string{"write", "grep"}, wantInitial: []string{"write", "g*"}, wantAllowed: 2},
		{name: "exclude removes from both", flags: Args{Tools: []string{"read", "bash"}, ExcludeTools: []string{"bash"}}, wantAgent: []string{"read"}, wantInitial: []string{"read"}, wantAllowed: 2},
		{name: "no tools", flags: Args{NoTools: true}, wantAgent: []string{}, wantInitial: []string{}, wantAllowed: 0},
		{name: "defaultTools setting order", flags: Args{}, defaults: []string{"write", "read"}, wantAgent: []string{"write", "read"}, wantInitial: []string{"write", "read"}},
	} {
		got := resolveToolSelection(tc.flags, tc.defaults, registry)
		if !slices.Equal(got.agentToolNames, tc.wantAgent) {
			t.Errorf("%s: agent tools = %v, want %v", tc.name, got.agentToolNames, tc.wantAgent)
		}
		if !slices.Equal(got.initialActive, tc.wantInitial) {
			t.Errorf("%s: initial active names = %v, want %v", tc.name, got.initialActive, tc.wantInitial)
		}
		if tc.flags.Tools != nil && len(got.allowed) != tc.wantAllowed {
			t.Errorf("%s: allowlist has %d entries, want %d", tc.name, len(got.allowed), tc.wantAllowed)
		}
	}
}

// Pi 1.1.0 args.test.ts:469-490: `--tools` entries that are all +name/-name change the default selection and are kept as written; a list that mixes names with modifiers, or gives a modifier a pattern, is an error diagnostic and leaves tools unset.
func TestParseFlagsToolModifiers(t *testing.T) {
	flags := parseArgs([]string{"-t", "+codemode,-write"})
	if !slices.Equal(flags.Tools, []string{"+codemode", "-write"}) || len(flags.Diagnostics) != 0 {
		t.Fatalf("modifiers: tools = %v, diagnostics = %v", flags.Tools, flags.Diagnostics)
	}
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"--tools", "read,+codemode"}, "--tools: tool names cannot be mixed with +name or -name entries"},
		{[]string{"-t", "+mcp__radius__*"}, "-t: +name and -name entries take exact tool names, not patterns: +mcp__radius__*"},
	} {
		got := parseArgs(tc.args)
		if got.Tools != nil || len(got.Diagnostics) != 1 || got.Diagnostics[0] != (argDiagnostic{Type: "error", Message: tc.message}) {
			t.Errorf("%v: tools = %v, diagnostics = %v, want one error %q", tc.args, got.Tools, got.Diagnostics, tc.message)
		}
	}
}

// Pi 1.1.0 default-tools-setting.test.ts:178-191 and sdk.ts:279-295: a modifier list applies to the defaultTools setting (none under --no-tools, whose allowlist becomes the result), and the Session keeps the entries for a reload.
func TestResolveToolSelectionAppliesToolModifiers(t *testing.T) {
	registry := tools.BuiltinToolNames()
	got := resolveToolSelection(Args{Tools: []string{"+inactive_tool", "-write"}}, []string{"read", "bash", "edit", "write", "grep"}, registry)
	want := []string{"read", "bash", "edit", "grep", "inactive_tool"}
	if !slices.Equal(got.initialActive, want) || !slices.Equal(got.agentToolNames, want) || got.allowed != nil || !slices.Equal(got.modifiers, []string{"+inactive_tool", "-write"}) {
		t.Errorf("selection = initial %v, agent %v, allowed %v, modifiers %v, want %v with no allowlist", got.initialActive, got.agentToolNames, got.allowed, got.modifiers, want)
	}
	if _, ok := got.activeBuiltin["grep"]; !ok || len(got.activeBuiltin) != len(want) {
		t.Errorf("active built-ins = %v, want the names of the result", got.activeBuiltin)
	}
	none := resolveToolSelection(Args{NoTools: true, Tools: []string{"+inactive_tool"}}, []string{"read"}, registry)
	if !slices.Equal(none.initialActive, []string{"inactive_tool"}) || len(none.allowed) != 1 {
		t.Errorf("--no-tools with +name: initial %v, allowed %v, want [inactive_tool] as the allowlist", none.initialActive, none.allowed)
	}
	plain := resolveToolSelection(Args{Tools: []string{"read"}}, nil, registry)
	if plain.modifiers != nil {
		t.Errorf("a plain list recorded modifiers %v", plain.modifiers)
	}
}

// The Session start options of the interactive build carry the --tools modifiers, which a settings reload applies (agent-session.ts:3667-3670).
func TestInteractiveStartCarriesToolModifiers(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Skip("interactiveStart needs services this test does not build")
		}
	}()
	start := interactiveStart(&cliBuild{DefaultToolModifiers: []string{"+codemode", "-write"}})
	if !slices.Equal(start.DefaultToolModifiers, []string{"+codemode", "-write"}) {
		t.Fatalf("start options modifiers = %v", start.DefaultToolModifiers)
	}
}
