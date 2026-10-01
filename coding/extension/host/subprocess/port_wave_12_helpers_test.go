package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func isolateExampleHost(t *testing.T) {
	t.Helper()
	// Resolve the installed runtime before changing HOME (PATH may contain a version-manager shim).
	node, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatalf("resolve Node executable: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(strings.TrimSpace(string(node)))+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	for _, name := range []string{"HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, path)
	}
	for _, name := range []string{"PI_SESSION_FILE", "PI_SESSION_ID", "PIG_SDK_GO_ROOT"} {
		t.Setenv(name, "")
	}
	// Socket paths must fit the platform limit independently of the test name and temporary HOME.
	shortSockDir(t)
}

func upstreamExamplePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("../../../..", ".upstream/current/packages/coding-agent/examples/extensions", name+".ts"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func loadExampleWithActions(t *testing.T, source string, actions *HostCallbacks) *extension.Extension {
	t.Helper()
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	bridge.SetActions(actions)
	host.SetUIBridge(bridge)
	ext, err := host.Load(t.Context(), ExtConfig{Name: "wave12-example", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return ext
}

func requireExampleHandler(t *testing.T, ext *extension.Extension, event string) extension.HandlerFn {
	t.Helper()
	handlers := ext.Handlers[event]
	if len(handlers) != 1 || handlers[0] == nil {
		t.Fatalf("%s: expected one callable handler, got %d", event, len(handlers))
	}
	return handlers[0]
}
