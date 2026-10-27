package extensionconformance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// lispFixtureName is the extension name of the Lisp SDK harness fixture (testdata/lisp-sdk-fixture), which every lane of the Lisp SDK
// extends with the probe commands of its family (probes-*.lisp), as the Python and Rust fixtures carry theirs.
const lispFixtureName = "lisp-sdk-fixture"

// lispHarnessCase is the Lisp SDK's case of the SDK harness: a test of a family the Lisp SDK serves appends it to sdkHarnessCases().
func lispHarnessCase() harnessCase {
	return harnessCase{name: "subprocess-lisp", make: makeSubprocessLispHarness, extName: lispFixtureName}
}

// lispFixtureConfig loads testdata/<dir> as the factory extension named name through the Lisp SDK.
func lispFixtureConfig(t testing.TB, dir, name string) subprocess.ExtConfig {
	t.Helper()
	return subprocess.ExtConfig{
		Name: name, Enabled: true, Isolation: "strict",
		Source:      filepath.Join(findModuleRoot(t), "test", "extension-conformance", "testdata", dir),
		Factory:     "new-extension",
		SDKName:     "pig-sdk-lisp",
		Package:     dir,
		RuntimeKind: "subprocess", RuntimeLanguage: "lisp", EntrypointKind: "factory",
	}
}

func makeSubprocessLispHarness(t *testing.T) *harness {
	t.Helper()
	notify := &[]string{}
	status := &[]string{}
	actions := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(hostEffects.notify)
	bridge.SetUIContext(ui)
	bridge.SetNotifyFunc(ui.RecordNotify)
	bridge.SetActions(conformanceActions(actions))

	h := subprocess.NewHost(t.TempDir())
	h.SetMode(harnessHostMode())
	h.SetHostInfo(conformanceHostInfo)
	h.SetUIBridge(bridge)
	wire := observeWire(t, h)

	// Host.Load builds from source with the Go and Rust toolchains only; the Lisp image is built through the cell loader.
	loaded, errs := h.LoadAll(context.Background(), []subprocess.ExtConfig{lispFixtureConfig(t, "lisp-sdk-fixture", lispFixtureName)})
	if len(errs) > 0 || len(loaded) != 1 {
		t.Fatalf("lisp subprocess load: %d extensions, errors %v", len(loaded), errs)
	}
	runner := inproc.NewRunner(loaded, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: h, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge, wire: wire}
}

// lispPackedHarnessCase is the Lisp SDK's packed case of the SDK harness: the fixture in one shared cell beside a peer extension.
func lispPackedHarnessCase() harnessCase {
	return harnessCase{name: "subprocess-lisp-packed", extName: lispFixtureName, make: func(t *testing.T) *harness { return makePackedUIHarness(t, "lisp") }}
}

// lispHarnessCases are both placements of the Lisp SDK, for a test that loops over append(sdkHarnessCases(), packedSDKHarnessCases()...).
func lispHarnessCases() []harnessCase {
	return []harnessCase{lispHarnessCase(), lispPackedHarnessCase()}
}
