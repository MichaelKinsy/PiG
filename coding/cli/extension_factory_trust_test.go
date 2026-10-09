package cli

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Negative test of docs/specs/extension-factory-trust.md (test 5), scoped to the routes a file extension has through the real subprocess host:
// a Node extension registers a tool, a command and a flag named after the compiled-in sentinel rows and subscribes to events, and the
// sentinel factories load exactly as often as they do without it. The positive control proves the counters count: the built-in row loads once
// per extension set, selected by `-e builtin:sentinel`, and the named inline row loads once.
func TestSubprocessExtensionCannotRunACompiledInFactory(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	builtinLoads, inlineLoads := 0, 0
	sentinels := []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "sentinel", Builtin: true, Factory: func(extension.API) error { builtinLoads++; return nil }},
		extension.NamedInlineExtension{Name: "sentinel", Factory: func(extension.API) error { inlineLoads++; return nil }},
	}
	load := func(flags Args) *extensionSetResult {
		t.Helper()
		builtinLoads, inlineLoads = 0, 0
		return reloadExtensionSet(t, newExtensionSetTestLoader(t, cwd, agentDir, flags, sentinels...), nil)
	}

	control := load(Args{Extensions: []string{"builtin:sentinel"}})
	if builtinLoads != 1 || inlineLoads != 1 {
		t.Fatalf("positive control: built-in sentinel loaded %d times and inline sentinel %d times, want 1 and 1", builtinLoads, inlineLoads)
	}
	baseline := control.paths()

	probe := `export default function (pi) {
  for (const name of ["builtin:sentinel", "<inline:sentinel>", "sentinel"]) {
    pi.registerTool({ name, label: name, description: name, parameters: { type: "object", properties: {} }, async execute() { return { content: [] }; } });
    pi.registerCommand(name, { description: name, handler: async () => {} });
    pi.registerFlag(name, { type: "boolean", description: name });
  }
  pi.on("session_start", async () => { pi.setActiveTools(["builtin:sentinel"]); });
}
`
	writeResourceTestFiles(t, root, map[string]string{"agent/extensions/probe.ts": probe})
	withProbe := load(Args{Extensions: []string{"builtin:sentinel"}})
	if builtinLoads != 1 || inlineLoads != 1 {
		t.Fatalf("with a subprocess extension registering sentinel names: built-in sentinel loaded %d times and inline sentinel %d times, want 1 and 1", builtinLoads, inlineLoads)
	}
	if len(withProbe.Errors) != 0 {
		t.Fatalf("errors = %+v", withProbe.Errors)
	}
	if got := withProbe.paths(); len(got) != len(baseline)+1 {
		t.Fatalf("extension paths = %q, want %q plus the probe", got, baseline)
	}
}

// TestReloadRunsBuiltInFactoriesAgainAndRetiresTheEarlierAPI is spec test 4 through the production extension set: a reload runs the
// compiled-in factory again, and the API the first run captured fails with the stale message while the new run's API works.
func TestReloadRunsBuiltInFactoriesAgainAndRetiresTheEarlierAPI(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	var captured []extension.API
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{NoExtensions: false},
		extension.NamedInlineExtension{Name: "sentinel", Builtin: true, Factory: func(pi extension.API) error { captured = append(captured, pi); return nil }})
	reloadExtensionSet(t, loader, nil)
	reloadExtensionSet(t, loader, nil)
	if len(captured) != 2 {
		t.Fatalf("the factory ran %d times, want 2", len(captured))
	}
	stale := func(pi extension.API) (message string) {
		defer func() {
			if recovered := recover(); recovered != nil {
				message = fmt.Sprint(recovered)
			}
		}()
		pi.GetFlag("x")
		return ""
	}
	if got := stale(captured[0]); got != extension.DefaultStaleRuntimeMessage {
		t.Errorf("the first run's API says %q, want the stale message", got)
	}
	if got := stale(captured[1]); got != "" {
		t.Errorf("the second run's API says %q", got)
	}
}
