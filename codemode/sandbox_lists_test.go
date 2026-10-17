package codemode_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// packages/codemode/src/types.ts:98-100 `globals` (CodemodeSandbox options: tool-like globals beside the built-in ones, in registration order). PiG-only readback: packages/codemode/test does not read sandbox.globals or sandbox.tools back, nor the name checks of the
// constructor options.
// Pi source: packages/codemode/src/runtime/host.ts
// mutation-checked: zeroing the results of Sandbox.Globals, Sandbox.Tools fails it
// Pi: packages/codemode/src/runtime/host.ts:111 (globals)
// Pi: packages/codemode/src/runtime/host.ts:25 (tools)
// packages/codemode/src/runtime/host.ts:111,131 (the sandbox host keeps tools and globals as separate maps).
func TestSandboxExposesRegisteredToolsAndGlobalsSeparatelyInOrder(t *testing.T) {
	noop := func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{
		Tools:   []codemode.Tool{{Name: "b", Execute: noop}, {Name: "a", Execute: noop}},
		Globals: []codemode.Tool{{Name: "models.list", Execute: noop}, {Name: "plain", Execute: noop}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	names := func(list []codemode.Tool) string {
		out := ""
		for _, tool := range list {
			out += tool.Name + ","
		}
		return out
	}
	if got := names(sandbox.Tools()); got != "b,a," {
		t.Fatalf("tools = %s", got)
	}
	if got := names(sandbox.Globals()); got != "models.list,plain," {
		t.Fatalf("globals = %s", got)
	}
	if err := sandbox.RegisterTool(codemode.Tool{Name: "b", Execute: noop}); err == nil || err.Error() != `Tool "b" is already registered` {
		t.Fatalf("duplicate tool err = %v", err)
	}
}

func TestSandboxRejectsInvalidReservedDuplicateAndConflictingGlobals(t *testing.T) {
	noop := func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	cases := map[string][]codemode.Tool{
		`Invalid global name "a.b.c"`:                   {{Name: "a.b.c", Execute: noop}},
		`Invalid global name "console"`:                 {{Name: "console", Execute: noop}},
		`Invalid global name "1x"`:                      {{Name: "1x", Execute: noop}},
		`Global "x" is already registered`:              {{Name: "x", Execute: noop}, {Name: "x", Execute: noop}},
		`Global "ns" conflicts with the namespace "ns"`: {{Name: "ns", Execute: noop}, {Name: "ns.f", Execute: noop}},
	}
	for want, globals := range cases {
		if _, err := codemode.NewSandbox(codemode.SandboxOptions{Globals: globals}); err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

// host.ts:381-387 `tools` and `globals` return fresh arrays ([...map.values()]), so changing a returned list does not change the sandbox; :396-398 execute() runs against
// `new Map(this.toolsByName)`, a snapshot, so a tool registered or unregistered while a script runs does not change what that script sees.
// mutation-checked: returning the sandbox's own slices from Tools or Globals fails it. Handing the live tool list to the execution passes, because newExecution copies the list into its own map when it starts and UnregisterTool never edits the list in place (the execution snapshot is redundant).
func TestSandboxListsAreCopiesAndAnExecutionSeesTheToolsRegisteredWhenItStarted(t *testing.T) {
	noop := func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`"noop"`), nil }
	var sandbox *codemode.Sandbox
	var err error
	sandbox, err = codemode.NewSandbox(codemode.SandboxOptions{
		Tools: []codemode.Tool{
			{Name: "b", Execute: noop},
			{Name: "swap", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				sandbox.UnregisterTool("b")
				if err := sandbox.RegisterTool(codemode.Tool{Name: "late", Execute: noop}); err != nil {
					return nil, err
				}
				return json.RawMessage(`"swapped"`), nil
			}},
		},
		Globals: []codemode.Tool{{Name: "g", Execute: noop}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })

	tools := sandbox.Tools()
	tools[0] = codemode.Tool{Name: "changed"}
	globals := sandbox.Globals()
	globals[0] = codemode.Tool{Name: "changed"}
	if got := sandbox.Tools()[0].Name; got != "b" {
		t.Errorf("changing the list Tools returned changed the sandbox: first tool %q", got)
	}
	if got := sandbox.Globals()[0].Name; got != "g" {
		t.Errorf("changing the list Globals returned changed the sandbox: first global %q", got)
	}

	result, err := sandbox.Execute(t.Context(), `await tools.swap(); let late = "ran"; try { await tools.late(); } catch { late = "absent"; } return [await tools.b(), late, ALL_TOOLS];`, codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || string(result.Value) != `["noop","absent",[{"name":"b","description":""},{"name":"swap","description":""}]]` {
		t.Fatalf("result = %+v (error %v), want the unregistered tool still callable and the late one absent in the running script", result, result.Error)
	}
	// The change is visible to the next execution.
	next, err := sandbox.Execute(t.Context(), `let b = "function"; try { await tools.b(); } catch { b = "undefined"; } return b + "," + await tools.late();`, codemode.ExecuteOptions{})
	if err != nil || !next.OK || string(next.Value) != `"undefined,noop"` {
		t.Fatalf("next execution = %+v, %v, want b gone and late registered", next, err)
	}
}
