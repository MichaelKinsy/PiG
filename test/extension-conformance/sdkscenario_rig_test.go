package extensionconformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// scenarioRig is the real production host a scenario's driver loads into: the
// subprocess host bound to a model registry of real session services.
type scenarioRig struct {
	sdk      sdkscenarios.SDK
	t        *testing.T
	root     string
	dir      string
	services *coding.AgentSessionServices
	host     *subprocess.Host
	bridge   *subprocess.UIBridge
	// calls records the host actions an extension triggered (abort, shutdown, compact), in order.
	calls hostCallLog
	// loaded are the extensions the rig loaded, in load order.
	loaded []extension.Extension
	// loadFailure holds the errors of a load that failed.
	loadFailure []error
	// configs are the extension configs a reload loads again.
	configs []subprocess.ExtConfig
	// footer is the data the host's footer reports.
	footer footerData
	// signal is the active run's cancellation the host reports as ctx.signal.
	signal runSignalState
}

// peerConfig loads the Node driver as the scenario's second extension. It is
// always the Node driver, whatever SDK the scenario tests: the second process
// that registers after the first.
func peerConfig(t *testing.T, root string) subprocess.ExtConfig {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for SDK conformance: %v", err)
	}
	return subprocess.ExtConfig{Name: "scenario-peer", Enabled: true, Isolation: "strict", Source: filepath.Join(root, "test", "extension-conformance", "testdata", "scenario-driver", "peer.mjs")}
}

// scenarioFile is the scenario's Spec, which the rig writes into the host's
// working directory (the extension process's working directory) so that rigs of
// different scenarios run in parallel without sharing an environment.
const scenarioFile = ".sdk-scenario.json"

// peerScenarioFile is the peer extension's Spec.
const peerScenarioFile = ".sdk-scenario-peer.json"

// scenarioDriverConfig is the extension config that loads the SDK's scenario
// driver. The driver reads its extension from scenarioFile.
func scenarioDriverConfig(t *testing.T, root string, sdk sdkscenarios.SDK, name string) subprocess.ExtConfig {
	t.Helper()
	base := filepath.Join(root, "test", "extension-conformance", "testdata")
	config := subprocess.ExtConfig{Name: name, Enabled: true, Isolation: "strict"}
	switch sdk {
	case sdkscenarios.Node:
		if _, err := exec.LookPath("node"); err != nil {
			t.Fatalf("node is required for SDK conformance: %v", err)
		}
		config.Source = filepath.Join(base, "scenario-driver", "driver.mjs")
		return config
	case sdkscenarios.Go:
		config.Source = filepath.Join(base, "scenario-driver-go")
		config.Factory = "Extension"
		config.SDKName = "github.com/MichaelKinsy/PiG/extensions/sdk"
		config.ModulePath = "example.com/scenario-driver-go"
		config.Package = config.ModulePath
	case sdkscenarios.Python:
		config.Source = filepath.Join(base, "scenario-driver-python")
		config.Factory = "new_extension"
		config.SDKName = "pig-sdk-py"
		config.Package = "scenario_driver"
	case sdkscenarios.Rust:
		config.Source = filepath.Join(base, "scenario-driver-rust")
		config.Factory = "new_extension"
		config.SDKName = "pig-sdk"
		config.Package = "scenario-driver-rust"
	default:
		t.Fatalf("unknown SDK %q", sdk)
	}
	config.RuntimeKind = "subprocess"
	config.RuntimeLanguage = string(sdk)
	config.EntrypointKind = "factory"
	return config
}

// newScenarioRig loads the scenario's extension through sdk's driver into a
// real host and registry.
func newScenarioRig(t *testing.T, sdk sdkscenarios.SDK, scenario sdkscenarios.Scenario) *scenarioRig {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeSpec(t, filepath.Join(dir, scenarioFile), scenario.Spec)
	if scenario.Peer != nil {
		writeSpec(t, filepath.Join(dir, peerScenarioFile), *scenario.Peer)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	bridge := subprocess.NewUIBridge(nil)
	host.SetUIBridge(bridge)
	rig := &scenarioRig{sdk: sdk, t: t, root: root, dir: dir, services: services, host: host, bridge: bridge}
	rig.installHostState()
	for _, command := range scenario.Spec.Commands {
		if command.Op == sdkscenarios.CommandFooterData {
			// A footer exists only where the host has a UI: the context reports hasUI and the extension's footer factory runs.
			rig.bridge.SetUIContext(rigUI{UIContext: extension.NoopUIContext})
		}
	}
	rig.configs = []subprocess.ExtConfig{scenarioDriverConfig(t, root, sdk, "scenario-driver-"+string(sdk))}
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(rig.configs), nil })
	loaded, errs := host.LoadAll(t.Context(), slices.Clone(rig.configs))
	if len(errs) != 0 {
		// The scenario observes a load that fails (an SDK rejecting the extension's
		// registration is behavior), so keep the cause for observe.
		rig.loadFailure = errs
		return rig
	}
	rig.loaded = loaded
	return rig
}

func writeSpec(t *testing.T, path string, spec sdkscenarios.Spec) {
	t.Helper()
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// primary is the SDK-under-test's extension as the host currently holds it.
func (r *scenarioRig) primary() extension.Extension {
	r.t.Helper()
	name := "scenario-driver-" + string(r.sdk)
	for _, ext := range r.loaded {
		if ext.Name == name {
			return ext
		}
	}
	r.t.Fatalf("the host holds no extension %s", name)
	return extension.Extension{}
}

// authJSON is a provider's auth.json entry, compacted; raw keeps the key order it was written in.
func (r *scenarioRig) authJSON(provider string, raw bool) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, "agent", "auth.json"))
	if os.IsNotExist(err) {
		return "<absent>"
	}
	if err != nil {
		r.t.Fatal(err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		r.t.Fatalf("auth.json: %v\n%s", err, data)
	}
	entry, ok := entries[provider]
	if !ok {
		return "<absent>"
	}
	if !raw {
		return canonicalJSON(r.t, entry)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, entry); err != nil {
		r.t.Fatal(err)
	}
	return compact.String()
}

// canonicalJSON is body with every object's keys sorted and numbers kept as written.
func canonicalJSON(t *testing.T, body []byte) string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("canonical JSON of %s: %v", body, err)
	}
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func scenariof(lines *[]string, format string, args ...any) {
	*lines = append(*lines, fmt.Sprintf(format, args...))
}
