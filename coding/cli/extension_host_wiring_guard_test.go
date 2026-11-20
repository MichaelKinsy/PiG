package cli

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
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

// checkHostCallbackBindings fails for every field mode leaves unbound without an allowance, and for every allowance whose field is bound, so a closed gap must delete its entry. It reports whether the ReloadContinuation allowance was used (the field unbound and allowed).
func checkHostCallbackBindings(t *testing.T, mode string, bridge *subprocess.UIBridge, allowed map[string]hostCallbackAllowance) bool {
	t.Helper()
	unbound := unboundHostCallbacks(t, bridge)
	for _, problem := range hostCallbackBindingProblems(mode, unbound, allowed) {
		t.Error(problem)
	}
	_, ok := allowed["ReloadContinuation"]
	return ok && slices.Contains(unbound, "ReloadContinuation")
}

// hostCallbackBindingProblems is the failure list checkHostCallbackBindings reports for the unbound fields of one mode.
func hostCallbackBindingProblems(mode string, unbound []string, allowed map[string]hostCallbackAllowance) []string {
	var problems []string
	for _, name := range unbound {
		if _, ok := allowed[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s mode leaves HostCallbacks.%s unbound: every extension that reaches it gets the host's fallback instead of the Session's answer", mode, name))
		}
	}
	for name, allowance := range allowed {
		if allowance.upstream == "" && allowance.gap == "" && allowance.deprecated == "" {
			problems = append(problems, fmt.Sprintf("%s mode allowance for HostCallbacks.%s has no reason", mode, name))
		}
		if !slices.Contains(unbound, name) {
			problems = append(problems, fmt.Sprintf("%s mode binds HostCallbacks.%s now; delete its allowance", mode, name))
		}
	}
	return problems
}

// The allowance is merged into the headless checks only: an interactive-mode check that leaves ReloadContinuation unbound still fails, and a headless check that leaves another field unbound still fails.
func TestReloadContinuationAllowanceCanFail(t *testing.T) {
	interactive := mergedAllowances(headlessFooterAllowances, deprecatedHostCallbackAllowances)
	headless := mergedAllowances(interactive, headlessReloadContinuationAllowances)
	has := func(problems []string, field string) bool {
		return slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, "HostCallbacks."+field+" unbound") })
	}
	t.Run("interactive still fails", func(t *testing.T) {
		if problems := hostCallbackBindingProblems("interactive", []string{"ReloadContinuation"}, interactive); !has(problems, "ReloadContinuation") {
			t.Errorf("problems = %v, want ReloadContinuation reported", problems)
		}
	})
	t.Run("headless allows it", func(t *testing.T) {
		if problems := hostCallbackBindingProblems("print", []string{"ReloadContinuation"}, headless); has(problems, "ReloadContinuation") {
			t.Errorf("problems = %v, want ReloadContinuation allowed", problems)
		}
	})
	t.Run("headless with another field unbound still fails", func(t *testing.T) {
		problems := hostCallbackBindingProblems("print", []string{"ReloadContinuation", "Shutdown"}, headless)
		if !has(problems, "Shutdown") || has(problems, "ReloadContinuation") {
			t.Errorf("problems = %v, want only Shutdown reported", problems)
		}
	})
}

// Pi's footer factory receives the footer data provider (types.ts:197-203); print mode binds no UI (runner.ts:323-335 noOpUIContext.setFooter) and RPC mode ignores the factory (rpc-mode.ts:210-212), so no extension reaches the footer data outside the TUI.
var headlessFooterAllowances = map[string]hostCallbackAllowance{
	"GetGitBranch":              {upstream: "runner.ts:335, rpc-mode.ts:210-212: no footer factory runs outside the TUI"},
	"GetExtensionStatuses":      {upstream: "runner.ts:335, rpc-mode.ts:210-212: no footer factory runs outside the TUI"},
	"GetAvailableProviderCount": {upstream: "runner.ts:335, rpc-mode.ts:210-212: no footer factory runs outside the TUI"},
}

// headlessReloadContinuationAllowances is the one callback print (text, json) and RPC mode leave unbound besides the others listed here. Headless modes reload via actions.Reload + SendUserMessage (D658); ReloadContinuation is an interactive-only deferral bound only by interactive mode (internal/codingagent/interactive_reload.go); Pi has no such callback (types.ts:454 `reload(): Promise<void>`).
var headlessReloadContinuationAllowances = map[string]hostCallbackAllowance{
	"ReloadContinuation": {gap: "D658: headless modes reload via actions.Reload + SendUserMessage; interactive-only deferral, no Pi counterpart (types.ts:454)"},
}

// The reload-continuation allowance names exactly one field, and a bare bridge still reports every other callback past it, so no other unbound field slips through.
func TestHeadlessReloadContinuationAllowanceIsNarrow(t *testing.T) {
	if names := slices.Collect(maps.Keys(headlessReloadContinuationAllowances)); !slices.Equal(names, []string{"ReloadContinuation"}) {
		t.Fatalf("allowance names = %v, want exactly [ReloadContinuation]", names)
	}
	unbound := unboundHostCallbacks(t, subprocess.NewUIBridge(func() {}))
	var unallowed []string
	for _, name := range unbound {
		if _, ok := headlessReloadContinuationAllowances[name]; !ok {
			unallowed = append(unallowed, name)
		}
	}
	if want := reflect.TypeFor[subprocess.HostCallbacks]().NumField() - 1; len(unallowed) != want {
		t.Fatalf("a bare bridge has %d unbound callbacks outside the allowance, want %d: %v", len(unallowed), want, unallowed)
	}
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
	used, modes := 0, 0
	defer func() { t.Logf("PARITY-ALLOWANCE ReloadContinuation-headless used=%d of=%d", used, modes) }()
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			host := printModeTestHost(t, ai.NewFauxProvider(ai.FauxConfig{}))
			bridge := subprocess.NewUIBridge(func() {})
			host.Bridge = bridge
			// The build binds the child session factory in every mode before the mode binds its own (cli_runtime_build.go); TestExtensionChildSessionsAcrossModes drives that path through the binary.
			BindChildSessions(bridge, host.Services)
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode})
			if result.err != nil {
				t.Fatalf("print mode: %v\n%s", result.err, result.stderr)
			}
			modes++
			if checkHostCallbackBindings(t, mode, bridge, mergedAllowances(headlessFooterAllowances, deprecatedHostCallbackAllowances, map[string]hostCallbackAllowance{
				"Shutdown": {upstream: "print-mode.ts:76-104 binds no shutdownHandler, so agent-session.ts:3365-3367 ctx.shutdown() does nothing"},
			}, headlessReloadContinuationAllowances)) {
				used++
			}
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
	// The build binds the child session factory in every mode before the mode binds its own (cli_runtime_build.go); TestExtensionChildSessionsAcrossModes drives that path through the binary.
	BindChildSessions(bridge, services)
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
	st := &rpcModeSession{}
	st.published.Store(&headlessCommandCatalog{})
	h := &rpcHost{writeRPC: func(any) {}, requestShutdown: func() {}}
	runner := inproc.NewRunner(nil, services.CWD())
	detach := h.bindExtensionActions(session, st, runner, func() *coding.Session { return session })
	defer detach()
	used := 0
	if checkHostCallbackBindings(t, "rpc", bridge, mergedAllowances(headlessFooterAllowances, deprecatedHostCallbackAllowances, headlessReloadContinuationAllowances)) {
		used++
	}
	t.Logf("PARITY-ALLOWANCE ReloadContinuation-headless used=%d of=1", used)
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
	diagnostics := flushExtensionHostRegistrations(host, services.Registry())
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
	if diagnostics := flushExtensionHostRegistrations(nil, services.Registry()); diagnostics != nil {
		t.Fatalf("a build without a host reported %+v", diagnostics)
	}
}

// A build flushes the provider registrations the host's extensions queued while they loaded the way Pi's createAgentSessionServices does (agent-session-services.ts:157-169): a registration the model registry rejects is the error diagnostic `Extension "<path>" error: <message>` and the others apply, whether or not the host bound the registry before the extensions loaded, as bindProviderRegistry binds it when the host is created. A registration an extension unregistered before the flush does not apply.
func TestBuildFlushesTheExtensionHostProviderRegistrations(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test complete")
	bindProviderRegistry(host, services.Registry().ModelRegistry)
	model := []extension.ProviderModelConfig{{ID: "m", Name: "M"}}
	queue := func(name, api string) {
		host.Runtime().QueueProviderRegistration(name, extension.ProviderConfig{API: ai.API(api), BaseURL: "https://" + name + ".invalid/v1", APIKey: "key", Models: model}, "/ext/"+name+".ts", nil)
	}
	queue("good", "openai-completions")
	queue("bad", "")
	queue("withdrawn", "openai-completions")
	host.Runtime().UnregisterProvider("withdrawn")
	if services.Registry().Find("good", "m") != nil {
		t.Fatal("a registration queued while an extension loaded reached the registry before the build flushed it")
	}
	diagnostics := flushExtensionHostRegistrations(host, services.Registry())
	want := []codingagent.AgentSessionRuntimeDiagnostic{{Type: "error", Message: `Extension "/ext/bad.ts" error: Provider bad, model m: no "api" specified. Set it at model level or provider level.`}}
	if !reflect.DeepEqual(diagnostics, want) {
		t.Fatalf("diagnostics = %+v, want %+v", diagnostics, want)
	}
	if services.Registry().Find("good", "m") == nil {
		t.Fatal("the accepted registration did not reach the registry")
	}
	if services.Registry().Find("bad", "m") != nil || services.Registry().Find("withdrawn", "m") != nil {
		t.Fatal("a rejected or unregistered registration reached the registry")
	}
	if pending := host.Runtime().PendingProviderRegistrations(); len(pending) != 0 {
		t.Fatalf("pending provider registrations after the build flushed them: %+v", pending)
	}
	// Later registrations apply at once and return their error to the extension (runner.ts:525-531).
	if err := host.Runtime().RegisterProvider("late", extension.ProviderConfig{BaseURL: "https://late.invalid/v1", Models: model}, "/ext/late.ts"); err == nil {
		t.Fatal("a rejected registration after the flush did not fail its caller")
	}
}
