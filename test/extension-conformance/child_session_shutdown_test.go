package extensionconformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

// A host that shuts down while child sessions are live leaves no process behind. The fixture's child_events tool builds sessions whose resource loaders start an extension host, once over the options and once over a DefaultResourceLoader the extension built, and disposes them without closing them: the extension's dispose does not end the session's host, and neither does the release of the loader's handle while a session holds it. Only Shutdown can.
func TestShutdownWithLiveChildSessionsLeavesNoDescendants(t *testing.T) {
	if _, err := os.ReadDir("/proc"); err != nil {
		t.Skip("counts descendant processes through /proc")
	}
	_, baseline := descendantRSS()
	host, services := newChildSessionHost(t)
	root := findModuleRoot(t)
	loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{{
		Name: childsession.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/child-session-go"), Enabled: true, Isolation: "strict",
		RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
		SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/child-session-go", Package: "example.com/child-session-go",
	}})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	checkChildExtensionEvents(t, services.CWD(), loaded[0])
	host.Shutdown("test complete")
	if _, left := descendantRSS(); left > baseline {
		t.Fatalf("%d descendant process(es) remain after Shutdown, %d before the host started", left, baseline)
	}
}
