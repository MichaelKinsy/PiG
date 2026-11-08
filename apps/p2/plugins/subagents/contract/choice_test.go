package contract

import (
	"context"
	"reflect"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the "tools given to agents" cases of apps/p2/test/p2/tool-access.test.ts (resolveTools).

type registered struct {
	name string
	reg  *durable.ToolRegistration
	p2runtime.ToolAccess
}

func tool(name string, callers []durable.ToolCaller, deferred bool, access p2runtime.ToolAccess) registered {
	return registered{name, &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name}, Callers: callers, Deferred: deferred,
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, nil
		},
	}, access}
}

func names(tools []*durable.ToolRegistration) []string {
	out := []string{}
	for _, each := range tools {
		out = append(out, each.Name)
	}
	return out
}

func TestToolsGivenToAgents(t *testing.T) {
	all := []registered{
		tool("read", nil, false, p2runtime.ToolAccess{}),
		tool("bash", nil, false, p2runtime.ToolAccess{}),
		tool("codemode", []durable.ToolCaller{durable.CallerModel}, false, p2runtime.ToolAccess{}),
		tool("agent", []durable.ToolCaller{durable.CallerTools}, false, p2runtime.ToolAccess{}),
		tool("mcp__files__echo", []durable.ToolCaller{durable.CallerTools}, true, p2runtime.ToolAccess{Origin: "owner"}),
		tool("powershell", nil, true, p2runtime.ToolAccess{}),
		tool("subagent", nil, false, p2runtime.ToolAccess{Scope: "main"}),
		tool("rename_session", nil, false, p2runtime.ToolAccess{Scope: "main"}),
		tool("team", nil, true, p2runtime.ToolAccess{Scope: "subagent"}),
	}
	access := map[string]p2runtime.P2Tool{}
	for _, each := range all {
		access[each.name] = p2runtime.P2Tool{ToolRegistration: each.reg, ToolAccess: each.ToolAccess}
	}
	pick := func(wanted ...string) []*durable.ToolRegistration {
		var out []*durable.ToolRegistration
		for _, each := range all {
			for _, name := range wanted {
				if each.name == name {
					out = append(out, each.reg)
				}
			}
		}
		return out
	}
	// The main agent: its model is offered these, `powershell` and `team` are deferred, scripts call the rest.
	caller := CallerTools{
		Tools:    pick("read", "bash", "codemode", "subagent", "rename_session"),
		Deferred: pick("powershell", "team"),
		Callable: pick("read", "bash", "agent", "mcp__files__echo", "subagent", "rename_session", "powershell", "team"),
	}
	equal := func(what string, got, want []string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}

	t.Run("gives a subagent every tool its caller may give by default, with the script-only ones and team", func(t *testing.T) {
		choice, problem := ResolveTools(caller, access, nil, false, nil, KindSubagent)
		if problem != "" {
			t.Fatal(problem)
		}
		equal("chosen", choice.Chosen, []string{"read", "bash", "codemode", "powershell"})
		equal("tools", names(choice.Tools), []string{"read", "bash", "codemode", "powershell", "agent", "mcp__files__echo", "team"})
		// Deferred tools stay deferred; `team` is offered from the start.
		equal("loaded", names(choice.Loaded), []string{"team"})
	})
	t.Run("narrows to the named tools in their order; named deferred ones are offered from the start", func(t *testing.T) {
		choice, problem := ResolveTools(caller, access, []string{"powershell", "read", "read"}, true, []string{"powershell", "read"}, KindOneShot)
		if problem != "" {
			t.Fatal(problem)
		}
		equal("chosen", choice.Chosen, []string{"powershell", "read"})
		// No code mode: no script-only tools; a one-shot agent gets no `team`.
		equal("tools", names(choice.Tools), []string{"powershell", "read"})
		equal("loaded", names(choice.Loaded), []string{"powershell"})
	})
	t.Run("refuses session-level, subagent-only, script-only and unknown tools", func(t *testing.T) {
		for _, name := range []string{"subagent", "rename_session", "team", "agent", "bogus"} {
			_, refused := ResolveTools(caller, access, []string{"read", name}, true, nil, KindSubagent)
			if !regexp.MustCompile(`^Unknown tools: ` + name + `\. Choose from your own tools: read, bash, codemode, powershell\.$`).MatchString(refused) {
				t.Errorf("%s: %q", name, refused)
			}
		}
	})
	t.Run("gives only what a limited caller has", func(t *testing.T) {
		limited := CallerTools{Tools: pick("read"), Callable: pick("read")}
		choice, problem := ResolveTools(limited, access, nil, false, nil, KindOneShot)
		if problem != "" {
			t.Fatal(problem)
		}
		equal("tools", names(choice.Tools), []string{"read"})
		if _, problem := ResolveTools(limited, access, []string{"bash"}, true, nil, KindOneShot); problem == "" {
			t.Error("a tool the caller lacks was given")
		}
	})
}
