package cli

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// unboundHostCallbacks lists the subprocess.HostCallbacks fields bridge leaves nil, in declaration order. Exec and ExecContext are one capability: either binding serves the exec call.
func unboundHostCallbacks(t *testing.T, bridge *subprocess.UIBridge) []string {
	t.Helper()
	field := reflect.ValueOf(bridge).Elem().FieldByName("actions")
	if !field.IsValid() || field.Kind() != reflect.Pointer {
		t.Fatal("subprocess.UIBridge no longer keeps its HostCallbacks in the actions field; update this guard")
	}
	typ := reflect.TypeFor[subprocess.HostCallbacks]()
	var unbound []string
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if typ.Field(i).Type.Kind() != reflect.Func {
			t.Fatalf("HostCallbacks.%s is not a callback; update this guard", name)
		}
		bound := !field.IsNil() && !field.Elem().Field(i).IsNil()
		if !bound && (name == "Exec" || name == "ExecContext") && !field.IsNil() {
			bound = !field.Elem().FieldByName("Exec").IsNil() || !field.Elem().FieldByName("ExecContext").IsNil()
		}
		if !bound {
			unbound = append(unbound, name)
		}
	}
	return unbound
}

// hostCallbackAllowance is a HostCallbacks field a mode leaves unbound, with the reason. upstream names the Pi source that leaves the capability unbound in that mode; gap names the lane that owns the missing binding; deprecated names why a published field no longer backs a capability.
type hostCallbackAllowance struct {
	upstream   string
	gap        string
	deprecated string
}

// checkHostCallbackBindings fails for every field mode leaves unbound without an allowance, and for every allowance whose field is bound, so a closed gap must delete its entry.
func checkHostCallbackBindings(t *testing.T, mode string, bridge *subprocess.UIBridge, allowed map[string]hostCallbackAllowance) {
	t.Helper()
	unbound := unboundHostCallbacks(t, bridge)
	for _, name := range unbound {
		if _, ok := allowed[name]; !ok {
			t.Errorf("%s mode leaves HostCallbacks.%s unbound: every extension that reaches it gets the host's fallback instead of the Session's answer", mode, name)
		}
	}
	for name, allowance := range allowed {
		if allowance.upstream == "" && allowance.gap == "" && allowance.deprecated == "" {
			t.Errorf("%s mode allowance for HostCallbacks.%s has no reason", mode, name)
		}
		if !slices.Contains(unbound, name) {
			t.Errorf("%s mode binds HostCallbacks.%s now; delete its allowance", mode, name)
		}
	}
}

// Pi's footer factory receives the footer data provider (types.ts:197-203); print mode binds no UI (runner.ts:323-335 noOpUIContext.setFooter) and RPC mode ignores the factory (rpc-mode.ts:210-212), so no extension reaches the footer data outside the TUI.
var headlessFooterAllowances = map[string]hostCallbackAllowance{
	"GetGitBranch":              {upstream: "runner.ts:335, rpc-mode.ts:210-212: no footer factory runs outside the TUI"},
	"GetExtensionStatuses":      {upstream: "runner.ts:335, rpc-mode.ts:210-212: no footer factory runs outside the TUI"},
	"GetAvailableProviderCount": {upstream: "runner.ts:335, rpc-mode.ts:210-212: no footer factory runs outside the TUI"},
}

// deprecatedHostCallbackAllowances are fields published in v0.2.0 that no extension call reads; they stay as deprecated aliases and no mode binds them.
var deprecatedHostCallbackAllowances = map[string]hostCallbackAllowance{
	"GetBranch":  {deprecated: "extensions read the branch through sessionRead or the session mirror"},
	"GetEntries": {deprecated: "extensions read the entries through sessionRead or the session mirror"},
}

func mergedAllowances(sets ...map[string]hostCallbackAllowance) map[string]hostCallbackAllowance {
	merged := map[string]hostCallbackAllowance{}
	for _, set := range sets {
		maps.Copy(merged, set)
	}
	return merged
}

// Print and JSON mode bind the Session to their extensions as print-mode.ts rebindSession does (print-mode.ts:74-104 over agent-session.ts:3275-3408 _bindExtensionCore). The guard runs the real mode and inspects the bridge its binding left behind.
func TestPrintModesBindEveryHostCallback(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			host := printModeTestHost(t, ai.NewFauxProvider(ai.FauxConfig{}))
			bridge := subprocess.NewUIBridge(func() {})
			host.Bridge = bridge
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode})
			if result.err != nil {
				t.Fatalf("print mode: %v\n%s", result.err, result.stderr)
			}
			checkHostCallbackBindings(t, mode, bridge, mergedAllowances(headlessFooterAllowances, deprecatedHostCallbackAllowances, map[string]hostCallbackAllowance{
				"Shutdown": {upstream: "print-mode.ts:76-104 binds no shutdownHandler, so agent-session.ts:3365-3367 ctx.shutdown() does nothing"},
			}))
		})
	}
}

// RPC mode binds the Session to its extensions as rpc-mode.ts rebindSession does (rpc-mode.ts:317-351 over agent-session.ts:3275-3408). The guard runs the binding RPC mode's bind uses.
func TestRPCModeBindsEveryHostCallback(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	// RPC mode reads the bridge from the Session's extension host.
	bridge := subprocess.NewUIBridge(func() {})
	runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services, ExtensionHost: &cliExtensionHost[struct{}]{bridge: bridge}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	session, err := runtime.New(coding.SessionStartOptions{
		Model:            &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}},
		SkipBuiltinTools: true,
		SessionDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	st := &rpcSessionState{Build: &cliBuild{CWD: services.CWD(), Services: services}}
	st.published.Store(&headlessCommandCatalog{})
	h := &rpcHost{writeRPC: func(any) {}, requestShutdown: func() {}}
	runner := inproc.NewRunner(nil, services.CWD())
	detach := h.bindExtensionActions(session, st, runner, func() *coding.Session { return session })
	defer detach()
	checkHostCallbackBindings(t, "rpc", bridge, mergedAllowances(headlessFooterAllowances, deprecatedHostCallbackAllowances))
}

// Every mode's build loads its extensions, then flushes the virtual models they queued into the model runtime before the local refresh and model resolution, as Pi's createAgentSessionServices does (agent-session-services.ts:182-193): a failed registration becomes the startup error diagnostic `Extension "<path>" error: <message>`, and later registrations and removals apply at once (runner.ts:497-541).
func TestBuildFlushesTheExtensionHostVirtualModels(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	physical := services.ModelRuntime().GetModels()
	if len(physical) == 0 {
		t.Fatal("the built-in catalog has no physical model to conflict with")
	}
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test complete")
	route := func(ctx context.Context, request extension.ModelRouteRequest) (extension.ModelRoute, error) {
		return extension.ModelRoute{}, nil
	}
	queued := extension.VirtualModelDefinition{Provider: "router", ID: "queued", Name: "Queued", Route: route}
	conflict := extension.VirtualModelDefinition{Provider: physical[0].ProviderMeta.ProviderID, ID: physical[0].ID, Name: "Conflict", Route: route}
	for _, definition := range []extension.VirtualModelDefinition{queued, conflict} {
		if err := host.Runtime().RegisterVirtualModel(definition, "/ext/router.ts"); err != nil {
			t.Fatal(err)
		}
	}
	diagnostics := flushExtensionHostVirtualModels(host, services.Registry())
	want := []codingagent.AgentSessionRuntimeDiagnostic{{Type: "error", Message: fmt.Sprintf(`Extension "/ext/router.ts" error: Virtual model %s/%s conflicts with a physical model.`, conflict.Provider, conflict.ID)}}
	if !reflect.DeepEqual(diagnostics, want) {
		t.Fatalf("diagnostics = %+v, want %+v", diagnostics, want)
	}
	if pending := host.Runtime().PendingVirtualModelRegistrations(); len(pending) != 0 {
		t.Fatalf("pending virtual models after the build flushed them: %+v", pending)
	}
	if model := services.ModelRuntime().GetModel("router", "queued"); model == nil {
		t.Fatal("the queued virtual model did not reach the model runtime")
	}
	late := extension.VirtualModelDefinition{Provider: "router", ID: "late", Name: "Late", Route: route}
	if err := host.Runtime().RegisterVirtualModel(late, "/ext/router.ts"); err != nil {
		t.Fatal(err)
	}
	if model := services.ModelRuntime().GetModel("router", "late"); model == nil {
		t.Fatal("a virtual model registered after the flush did not reach the model runtime")
	}
	if err := host.Runtime().RegisterVirtualModel(conflict, "/ext/router.ts"); err == nil {
		t.Fatal("a conflicting registration after the flush did not fail its caller")
	}
	host.Runtime().UnregisterVirtualModel("router", "late")
	if model := services.ModelRuntime().GetModel("router", "late"); model != nil {
		t.Fatal("unregistering a virtual model after the flush left it in the model runtime")
	}
	if diagnostics := flushExtensionHostVirtualModels(nil, services.Registry()); diagnostics != nil {
		t.Fatalf("a build without a host reported %+v", diagnostics)
	}
}
