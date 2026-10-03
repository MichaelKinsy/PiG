package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Guards the TestMain temporary-directory scope: this package's tests must not write scratch files or directories into the shared temporary directory, where a killed or forgetful test leaves them for good.
func TestTempDirIsScopedToThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-sp-")
}

// An extension host writes its stderr diagnostic to a pig-ext-*.log file in os.TempDir and removes it only on a clean shutdown. A host a test never shuts down, or a crashed extension whose log the host keeps on purpose, must leave that file under the scoped directory TestMain removes.
func TestExtensionHostLogsStayInScopedTempDir(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-mode.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, err := h.Load(t.Context(), ExtConfig{Name: "ctx-mode", Source: fixture, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	logPath := h.exts["ctx-mode"].stderrLogPath
	h.mu.Unlock()
	if logPath == "" || filepath.Dir(logPath) != os.TempDir() {
		t.Fatalf("extension stderr log %q is outside the scoped temporary directory %q", logPath, os.TempDir())
	}
}
