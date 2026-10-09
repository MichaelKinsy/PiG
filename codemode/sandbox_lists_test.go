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
