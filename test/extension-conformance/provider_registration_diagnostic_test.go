//go:build !pig_strip_node_extensions

package extensionconformance

import (
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

// Pi's loader queues the registrations an extension makes while it loads, and the services that bind the model runtime apply them after every extension finished: a registration the
// model runtime rejects is the diagnostic `Extension "<path>" error: <message>`, the extension stays loaded, and the other registrations apply (loader.ts:217-218,
// agent-session-services.ts:157-169). Every SDK registers one accepted and one rejected provider while it loads, isolated and packed, into a host bound to a registry before it loads, as the CLI binds
// its host (the host used to ask the registry during the load and fail the extension with the registry's message).
//
// mutation-checked: making the host load registration call the bound registry at once (Host.queueLoadedProvider calling providerRuntime.RegisterProvider) fails every case with a failed load; dropping the Node runtime's `loadState === "active"` guard before validateExtensionProvider fails the Node cases.
func TestProviderRegistrationFailureIsADiagnosticAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	cases := []struct {
		name, source, sibling string
		fused                 bool
		packed                bool
	}{
		{"go-isolated", "testdata/provider-diag-go", "", false, false}, {"go-packed", "testdata/provider-diag-go", "testdata/provider-go-sibling", false, true},
		{"python-isolated", "testdata/provider-diag-python", "", false, false}, {"python-packed", "testdata/provider-diag-python", "testdata/provider-python-sibling", false, true},
		{"rust-isolated", "testdata/provider-diag-rust", "", false, false}, {"rust-packed", "testdata/provider-diag-rust", "testdata/provider-rust-sibling", false, true},
		{"node-isolated", "testdata/provider-diag.mjs", "", false, false}, {"node-packed", "testdata/provider-diag.mjs", "testdata/provider-sibling.mjs", false, true},
		{"fused-go", "", "", true, false},
	}
	const rejected = `Provider diag-bad, model bad-model: no "api" specified. Set it at model level or provider level.`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			host := subprocess.NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
			host.SetUIBridge(subprocess.NewUIBridge(func() {}))
			var loaded []extension.Extension
			var loadErrors []error
			if tc.fused {
				ext, loadErr := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "provider-diag", Path: "/ext/provider-diag.go", Enabled: true}, func(conn net.Conn) error { return testfixture.ProviderDiag().RunWithConn(conn) })
				if loadErr != nil {
					loadErrors = append(loadErrors, loadErr)
				} else {
					loaded = append(loaded, *ext)
				}
			} else {
				source, resolveErr := filepath.Abs(tc.source)
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				cfg, _, resolveErr := subprocess.ResolveExtConfigWithIdentity(source, "provider-diag")
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				cfgs := []subprocess.ExtConfig{cfg}
				if tc.packed {
					sibling, pathErr := filepath.Abs(tc.sibling)
					if pathErr != nil {
						t.Fatal(pathErr)
					}
					peer, _, siblingErr := subprocess.ResolveExtConfigWithIdentity(sibling, "provider-sibling")
					if siblingErr != nil {
						t.Fatal(siblingErr)
					}
					cfgs = append(cfgs, peer)
				} else {
					cfgs[0].Isolation = "isolated"
				}
				loaded, loadErrors = host.LoadAll(t.Context(), cfgs)
			}
			if len(loadErrors) != 0 {
				t.Fatalf("the extension must load: a rejected provider registration is a diagnostic, not a failed load: %v", loadErrors)
			}
			ext := loaded[0]
			if _, ok := ext.Tools["diag_ping"]; !ok {
				t.Fatalf("the extension's own registrations did not load: tools %v", ext.Tools)
			}
			// Pi's loader queues: the registry has been asked for nothing while the extensions loaded.
			if services.Registry().Find("diag-good", "good-model") != nil {
				t.Fatal("the registry held a provider the extension registered while it loaded, before the services applied the queue")
			}
			runtime := host.Runtime()
			var queued []string
			for _, pending := range runtime.PendingProviderRegistrations() {
				queued = append(queued, pending.Name)
			}
			if want := []string{"diag-good", "diag-bad"}; !reflect.DeepEqual(queued, want) {
				t.Fatalf("queued registrations = %v, want %v in registration order", queued, want)
			}
			// The services apply the queue and word the diagnostics (agent-session-services.ts:157-169).
			var diagnostics []string
			runtime.BindProviderActions(runtime.BoundProviderActions(), func(err *extension.ExtensionError) {
				diagnostics = append(diagnostics, fmt.Sprintf(`Extension "%s" error: %s`, err.ExtensionPath, err.Error))
			})
			if want := []string{fmt.Sprintf(`Extension "%s" error: %s`, ext.Path, rejected)}; !reflect.DeepEqual(diagnostics, want) {
				t.Fatalf("diagnostics = %q, want %q", diagnostics, want)
			}
			if services.Registry().Find("diag-good", "good-model") == nil {
				t.Fatal("the accepted registration did not apply")
			}
			if services.Registry().Find("diag-bad", "bad-model") != nil {
				t.Fatal("the rejected registration applied")
			}
			if left := runtime.PendingProviderRegistrations(); len(left) != 0 {
				t.Fatalf("registrations left in the queue after the services applied it: %v", left)
			}
		})
	}
}
